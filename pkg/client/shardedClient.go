package client

import (
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aradilov/fastrpc"
	"github.com/aradilov/uniqid"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/internal/sdkutil"
	"github.com/mygaru/dcr-sdk/pkg/contract"
	"google.golang.org/protobuf/proto"
)

const defaultCloudAddr = "cloud.mygaru.com:7937"

type Configuration struct {
	// Addrs specifies the comma-separated list of server addresses used for sharding the client connections.
	Addrs string

	// Client's JWT token for authentication.
	JwtToken []byte

	// MaxRequestDuration specifies the maximum duration allowed for each request to prevent excessive timeouts or delays.
	MaxRequestDuration time.Duration

	// MaxDialDuration specifies the maximum duration allowed for establishing a connection before timing out.
	MaxDialDuration time.Duration

	// DNSRefreshInterval specifies how often hostname addresses are re-resolved.
	// If zero, a default refresh interval is used. If negative, periodic refresh is disabled.
	DNSRefreshInterval time.Duration

	// MaxPendingRequests is the maximum number of pending requests
	// the client may issue until the server responds to them.
	MaxPendingRequests int

	// MaximumSimultaneousConnections specifies the maximum number of connections that can be active simultaneously for the server.
	// By default MaximumSimultaneousConnections is tuned for low-latency high-throughput traffic.
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

	// clients is a slice of pointers to client instances used for managing connections to multiple servers for sharding.
	clients []*clientsGroup
}

// clientsGroup is a structure that holds a group of client instances for managing sharded connections to the signle server.
type clientsGroup struct {
	// roundRobin is a counter used for load balancing to distribute requests evenly across client connections.
	roundRobin uint64
	clients    []*client
	id         atomic.Uint32
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
	shard := sc.getGroup()
	cl := shard.getClient()

	res, statusCode, err := cl.doUnary(req, resp, contract.Mock)
	if nil != err {
		return nil, statusCode, err
	}

	shard.id.Store(uint32(cl.serverID))
	return res, statusCode, nil
}

// Target is the primary request used by a third-party platform to:
// Verify if the user belongs to specific segments;
// Check frequency capping compliance by key;
// Obtain identification accuracy to determine the validity and reliability of the response.
// For more details, see here: [LINK]
func (sc *ShardedClient) Target(req *base.TargetRequest) (*base.TargetResponse, base.RPCServerResponseCode, error) {
	shard := sc.getGroup()
	cl := shard.getClient()
	res, statusCode, err := cl.doUnary(req, &base.TargetResponse{}, contract.Target)
	if nil != err {
		return nil, statusCode, err
	}
	shard.id.Store(uint32(cl.serverID))
	return res.(*base.TargetResponse), statusCode, nil
}

// Report is used by a third-party platform to report that a specific event has occurred.
// This mechanism is used to record statistical data and perform settlements between system users as part of the third-party billing strategy.
// For more details, see here: [LINK]
func (sc *ShardedClient) Report(req *base.ReportRequest) (base.RPCServerResponseCode, error) {
	if nil == req.TrackingId {
		return base.RPCServerResponseCode_UNKNOWN, fmt.Errorf("tracking id is required")
	}
	shard := sc.lookupGroup(uniqid.GetServerID(req.TrackingId))
	if nil == shard {
		return base.RPCServerResponseCode_UNKNOWN, fmt.Errorf("unknown server for tracking id: %q", req.TrackingId)
	}

	_, statusCode, err := shard.getClient().doUnary(req, nil, contract.Report)
	return statusCode, err
}

// IsValidTrackingID checks if the provided tracking ID is valid by ensuring it is not nil and maps to a valid shard.
func (sc *ShardedClient) IsValidTrackingID(trackingId []byte) bool {
	if nil == trackingId {
		return false
	}
	shard := sc.lookupGroup(uniqid.GetServerID(trackingId))
	return nil != shard
}

func (sc *ShardedClient) lookupGroup(serverID uint16) *clientsGroup {
	var shard *clientsGroup
	for i := 0; i < len(sc.clients); i++ {
		shard = sc.clients[i]
		shardID := uint16(shard.id.Load())
		if shardID > 0 && shardID == serverID {
			return shard
		}
	}

	return nil
}

// getGroup selects a clientsGroup instance from the sharded clients list using a round-robin load-balancing strategy.
func (sc *ShardedClient) getGroup() *clientsGroup {
	n := atomic.AddUint64(&sc.roundRobin, 1)
	idx := n % uint64(len(sc.clients))

	return sc.clients[idx]
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
	defaultMaxRequestDuration             = time.Second
	defaultMaximumSimultaneousConnections = 128
	defaultMaxPendingRequests             = 8
	defaultBufferSize                     = 4 * 1024
)

// NewClient initializes and returns a new instance of ShardedClient.
func NewClient(cfg *Configuration, tlsConfig *tls.Config) *ShardedClient {
	cfg = normalizeConfiguration(cfg)

	sc := &ShardedClient{Configuration: *cfg}

	for _, shardAddr := range strings.Split(cfg.Addrs, ",") {
		shardAddr = strings.TrimSpace(shardAddr)
		if shardAddr == "" {
			continue
		}

		metrics := buildShardMetrics(shardAddr)
		shard := &clientsGroup{}
		dialer := newDNSDialer(shardAddr, cfg.MaxDialDuration, cfg.DNSRefreshInterval)

		for i := 0; i < cfg.MaximumSimultaneousConnections; i++ {
			rpc := &client{
				maxRequestDuration: cfg.MaxRequestDuration,
				metricGroups:       metrics,
				JwtToken:           cfg.JwtToken,
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
					WriteTimeout:       cfg.MaxRequestDuration * 10,
					MaxPendingRequests: cfg.MaxPendingRequests,
					CompressType:       fastrpc.CompressSnappy,
					WriteBufferSize:    cfg.WriteBufferSize,
					ReadBufferSize:     cfg.ReadBufferSize,
				},
			}

			rpcRef := rpc
			rpc.c.Dial = func(addr string) (net.Conn, error) {
				conn, err := dialer.dial(rpcRef)
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
	normalized := *cfg
	if normalized.MaximumSimultaneousConnections <= 0 {
		normalized.MaximumSimultaneousConnections = defaultMaximumSimultaneousConnections
	}
	normalized.Addrs = normalizeAddrs(normalized.Addrs)
	if len(normalized.Addrs) == 0 {
		normalized.Addrs = defaultCloudAddr
	}
	if normalized.MaxRequestDuration <= 0 {
		normalized.MaxRequestDuration = defaultMaxRequestDuration
	}
	if normalized.MaxDialDuration <= 0 {
		normalized.MaxDialDuration = normalized.MaxRequestDuration
	}
	if normalized.DNSRefreshInterval == 0 {
		normalized.DNSRefreshInterval = defaultDNSRefreshInterval
	}
	if normalized.MaxPendingRequests <= 0 {
		normalized.MaxPendingRequests = defaultMaxPendingRequests
	}
	if normalized.ReadBufferSize <= 0 {
		normalized.ReadBufferSize = defaultBufferSize
	}
	if normalized.WriteBufferSize <= 0 {
		normalized.WriteBufferSize = defaultBufferSize
	}
	return &normalized
}

func normalizeAddrs(addrs string) string {
	var normalized []string
	for _, addr := range strings.Split(addrs, ",") {
		addr = strings.TrimSpace(addr)
		if addr != "" {
			normalized = append(normalized, addr)
		}
	}
	return strings.Join(normalized, ",")
}

func buildShardMetrics(shardAddr string) [contract.MaxRequestIdentifier + 1]*metricsGroup {
	var metrics [contract.MaxRequestIdentifier + 1]*metricsGroup
	for i := 1; i <= int(contract.MaxRequestIdentifier); i++ {
		req := contract.RPCRegister(i)
		metrics[i] = newMetricsGroup(req.String(), shardAddr)
	}
	return metrics
}
