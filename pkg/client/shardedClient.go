package client

import (
	"crypto/tls"
	"github.com/aradilov/fastrpc"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/internal/contract"
	"github.com/mygaru/dcr-sdk/internal/sdkutil"
	"github.com/valyala/fasthttp"
	"google.golang.org/protobuf/proto"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const defaultCloudAddr = "cloud.mygaru.com:7943"

type Configuration struct {
	// Addrs specifies the comma-separated list of server addresses used for sharding the client connections.
	Addrs string

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
	clients []*client
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

func (sc *ShardedClient) getClient() *client {
	n := atomic.AddUint64(&sc.roundRobin, 1)
	idx := n % uint64(len(sc.clients))

	return sc.clients[idx]
}

func (sc *ShardedClient) PendingRequests() int {
	n := 0
	for _, c := range sc.clients {
		n += c.c.PendingRequests()
	}
	return n
}

// NewClient initializes and returns a new instance of ShardedClient
func NewClient(cfg *Configuration, tls *tls.Config) *ShardedClient {
	if nil == cfg {
		cfg = &Configuration{}
	}

	if 0 == len(cfg.Addrs) {
		cfg.Addrs = defaultCloudAddr
	}

	sc := &ShardedClient{
		Configuration: *cfg,
	}

	maxRequestDuration := sc.MaxRequestDuration
	maxDialDuration := sc.MaxDialDuration
	maxPendingRequests := sc.MaxPendingRequests
	readBufferSize := sc.ReadBufferSize
	writeBufferSize := sc.WriteBufferSize

	if maxRequestDuration <= 0 {
		maxRequestDuration = time.Second
	}

	if maxDialDuration == 0 {
		maxDialDuration = maxRequestDuration
	}

	if maxPendingRequests == 0 {
		maxPendingRequests = 4e3
	}

	if readBufferSize == 0 {
		readBufferSize = 4 * 1024
	}

	if writeBufferSize == 0 {
		writeBufferSize = 4 * 1024
	}

	for _, shardAddr := range strings.Split(cfg.Addrs, ",") {
		shard := &client{
			maxRequestDuration: maxRequestDuration,

			c: &fastrpc.Client{
				SniffHeader:     sdkutil.SniffHeader,
				ProtocolVersion: sdkutil.ProtocolVersion,

				NewResponse: func() fastrpc.ResponseReader {
					return &contract.Response{}
				},

				Dial: func(addr string) (conn net.Conn, err error) {
					return fasthttp.DialTimeout(addr, maxDialDuration)
				},

				TLSConfig: tls,

				Addr: shardAddr,

				// read timeout should be quite high in order to avoid
				// frequent reconnects for almost idle connections.
				ReadTimeout: time.Minute,

				WriteTimeout:       maxRequestDuration * 10,
				MaxPendingRequests: maxPendingRequests,

				CompressType: fastrpc.CompressSnappy,

				WriteBufferSize: writeBufferSize,
				ReadBufferSize:  readBufferSize,
			},
		}

		for i := 1; i <= int(contract.MaxRequestIdentifier); i++ {
			reqn := contract.RPCRegister(i)
			shard.metricGroups[i] = newMetricsGroup(shardAddr, reqn.String())
		}

		sc.clients = append(sc.clients, shard)
	}

	return sc
}
