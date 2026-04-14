package client

import (
	"crypto/tls"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aradilov/fastrpc"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/internal/contract"
	"github.com/mygaru/dcr-sdk/internal/sdkutil"
	"github.com/valyala/fasthttp"
	"google.golang.org/protobuf/proto"
)

const defaultCloudAddr = "cloud.mygaru.com:7943"

type Configuration struct {
	// Addrs specifies the comma-separated list of server addresses used for sharding the client connections.
	Addrs string

	// Client's JVT token for authentication.
	JvtToken []byte

	// MaxRequestDuration specifies the maximum duration allowed for each request to prevent excessive timeouts or delays.
	MaxRequestDuration time.Duration

	// MaxDialDuration specifies the maximum duration allowed for establishing a connection before timing out.
	MaxDialDuration time.Duration

	// MaxPendingRequests is the maximum number of pending requests
	// the client may issue until the server responds to them.
	MaxPendingRequests int

	// MaximumSimultaneousConnections specifies the maximum number of connections that can be active simultaneously for the server.
	// Bu default MaximumSimultaneousConnections is equal to the number of threads
	MaximumSimultaneousConnections int

	// ReadBufferSize is the size for read buffer.
	//
	// DefaultReadBufferSize is used by default.
	ReadBufferSize int

	// WriteBufferSize is the size for write buffer.
	//
	// DefaultWriteBufferSize is used by default.
	WriteBufferSize int
}

type ShardedClient struct {
	Configuration

	// roundRobin is a counter used for load balancing to distribute requests evenly across client connections.
	roundRobin uint64

	once sync.Once

	// clients is a slice of pointers to client instances used for managing connections to multiple servers for sharding.
	clients []clientsGroup
}

// clientsGroup represents a structure containing a list of clients and a counter for distributing requests via round-robin.
type clientsGroup struct {
	// roundRobin is a counter used for load balancing to distribute requests evenly across client connections.
	roundRobin uint64
	clients    []*client
}

// getClient returns a pointer to the next client in the clientsGroup's clients list using a round-robin load-balancing strategy.
func (sh *clientsGroup) getClient() *client {
	n := atomic.AddUint64(&sh.roundRobin, 1)
	idx := n % uint64(len(sh.clients))

	return sh.clients[idx]
}

// Mock sends a mock request and returns the response, status code, and any error encountered during execution.
// For debug usage only
func (sc *ShardedClient) Mock(req *base.MockRequest, resp proto.Message) (proto.Message, base.RPCServerResponseCode, error) {
	res, statusCode, err := sc.getClient().doUnary(req, resp, contract.Mock)
	if nil != err {
		return nil, statusCode, err
	}
	return res, statusCode, nil
}

// Target is the primary request used by a third-party platform to:
// Verify if the user belongs to specific segments;
// Check frequency capping compliance by key;
// Obtain identification accuracy to determine the validity and reliability of the response.
// For more details, see here: [LINK]
func (sc *ShardedClient) Target(req *base.TargetRequest) (*base.TargetResponse, base.RPCServerResponseCode, error) {
	res, statusCode, err := sc.getClient().doUnary(req, &base.TargetResponse{}, contract.Target)
	if nil != err {
		return nil, statusCode, err
	}
	return res.(*base.TargetResponse), statusCode, nil
}

// Report is used by a third-party platform to report that a specific event has occurred.
// This mechanism is used to record statistical data and perform settlements between system users as part of the third-party billing strategy.
// For more details, see here: [LINK]
func (sc *ShardedClient) Report(req *base.ReportRequest) (base.RPCServerResponseCode, error) {
	_, statusCode, err := sc.getClient().doUnary(req, nil, contract.Report)
	return statusCode, err
}

// getClient returns the next client instance from the sharded client's list using a round-robin load-balancing mechanism.
func (sc *ShardedClient) getClient() *client {
	n := atomic.AddUint64(&sc.roundRobin, 1)
	idx := n % uint64(len(sc.clients))

	return sc.clients[idx].getClient()
}

// PendingRequests computes the total number of pending requests across all clients managed by the ShardedClient instance.
func (sc *ShardedClient) PendingRequests() int {
	n := 0
	for _, c := range sc.clients {
		for i := 0; i < len(c.clients); i++ {
			n += c.clients[i].c.PendingRequests()
		}

	}
	return n
}

// Reconnects computes and returns the total number of reconnections across all clients in the ShardedClient instance.
func (sc *ShardedClient) Reconnects() int {
	n := 0
	for _, c := range sc.clients {
		for i := 0; i < len(c.clients); i++ {
			n += int(c.clients[i].Reconnects())
		}

	}
	return n
}

// NewClient initializes and returns a new instance of ShardedClient
const (
	defaultMaxRequestDuration = time.Second
	defaultMaxPendingRequests = 4_000
	defaultBufferSize         = 4 * 1024
)

// NewClient initializes and returns a new instance of ShardedClient.
func NewClient(cfg *Configuration, tlsConfig *tls.Config) *ShardedClient {
	cfg = normalizeConfiguration(cfg)

	sc := &ShardedClient{Configuration: *cfg}

	maxRequestDuration := sc.MaxRequestDuration
	if maxRequestDuration <= 0 {
		maxRequestDuration = defaultMaxRequestDuration
	}

	maxDialDuration := sc.MaxDialDuration
	if maxDialDuration == 0 {
		maxDialDuration = maxRequestDuration
	}

	maxPendingRequests := sc.MaxPendingRequests
	if maxPendingRequests == 0 {
		maxPendingRequests = defaultMaxPendingRequests
	}

	readBufferSize := sc.ReadBufferSize
	if readBufferSize == 0 {
		readBufferSize = defaultBufferSize
	}

	writeBufferSize := sc.WriteBufferSize
	if writeBufferSize == 0 {
		writeBufferSize = defaultBufferSize
	}

	for _, shardAddr := range strings.Split(cfg.Addrs, ",") {
		shardAddr = strings.TrimSpace(shardAddr)
		if shardAddr == "" {
			continue
		}

		metrics := buildShardMetrics(shardAddr)
		shard := clientsGroup{}

		for i := 0; i < cfg.MaximumSimultaneousConnections; i++ {
			rpc := &client{
				maxRequestDuration: maxRequestDuration,
				metricGroups:       metrics,
				JvtToken:           cfg.JvtToken,
				c: &fastrpc.Client{
					SniffHeader:     sdkutil.SniffHeader,
					ProtocolVersion: sdkutil.ProtocolVersion,
					NewResponse: func() fastrpc.ResponseReader {
						return &contract.Response{}
					},
					TLSConfig: tlsConfig,
					Addr:      shardAddr,
					// High-read timeout helps avoid frequent reconnects on mostly idle connections.
					ReadTimeout:        time.Minute,
					WriteTimeout:       maxRequestDuration * 10,
					MaxPendingRequests: maxPendingRequests,
					CompressType:       fastrpc.CompressSnappy,
					WriteBufferSize:    writeBufferSize,
					ReadBufferSize:     readBufferSize,
				},
			}

			rpcRef := rpc
			rpc.c.Dial = func(addr string) (net.Conn, error) {
				conn, err := fasthttp.DialTimeout(addr, maxDialDuration)
				if err != nil {
					return nil, err
				}
				rpcRef.connGen.Add(1)
				return conn, nil
			}

			shard.clients = append(shard.clients, rpc)
		}

		sc.clients = append(sc.clients, shard)
	}

	return sc
}

func normalizeConfiguration(cfg *Configuration) *Configuration {
	if cfg == nil {
		cfg = &Configuration{}
	}
	if cfg.MaximumSimultaneousConnections <= 0 {
		cfg.MaximumSimultaneousConnections = runtime.GOMAXPROCS(-1)
	}
	if len(cfg.Addrs) == 0 {
		cfg.Addrs = defaultCloudAddr
	}
	return cfg
}

func buildShardMetrics(shardAddr string) [contract.MaxRequestIdentifier + 1]*metricsGroup {
	var metrics [contract.MaxRequestIdentifier + 1]*metricsGroup
	for i := 1; i <= int(contract.MaxRequestIdentifier); i++ {
		req := contract.RPCRegister(i)
		metrics[i] = newMetricsGroup(shardAddr, req.String())
	}
	return metrics
}
