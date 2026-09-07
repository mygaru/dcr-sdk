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
	"github.com/google/uuid"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/internal/sdkutil"
	"github.com/mygaru/dcr-sdk/pkg/contract"
)

const defaultCloudAddr = "cloud.mygaru.com:7937"

type Configuration struct {
	// Addrs specifies the comma-separated list of server addresses used for sharding the client connections.
	Addrs string

	// JwtToken was the client's JWT for the connection-level contract.Auth
	// handshake.
	//
	// Deprecated: the SDK no longer authenticates connections. The token is now
	// only read once, at construction time, to recover partner.id as the default
	// payer for calls that pass uuid.Nil. Pass the payer to Target/Report
	// explicitly and stop setting this field.
	JwtToken []byte

	// DisableAuth skipped the legacy contract.Auth request.
	//
	// Deprecated: no-op. Connection authentication has been removed, so there is
	// nothing left to disable. Kept so existing configurations still compile.
	DisableAuth bool

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

	// legacyPayer is partner.id recovered from the deprecated
	// Configuration.JwtToken at construction time. It is the last-resort default
	// for calls that pass uuid.Nil and leave the request payer empty.
	legacyPayer uuid.UUID
}

// clientsGroup holds the connections opened to one address from Addrs.
//
// It carries no node identity. Behind a load balancer a single address serves
// several cloud nodes, so one id per group could never represent them; the cloud
// routes a report to the node that holds its request context by itself.
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

// Target is the primary request used by a third-party platform to:
// Verify if the user belongs to specific segments;
// Check frequency capping compliance by key;
// Obtain identification accuracy to determine the validity and reliability of the response.
// For more details, see here: [LINK]
//
// Each match rule names the client whose segments are checked, in
// req.Match[i].Payer. A rule that leaves it empty falls back to the partner id
// of the deprecated Configuration.JwtToken. A request with no match rules needs
// no payer at all - it only resolves the identifiers and returns a tracking id.
func (sc *ShardedClient) Target(req *base.TargetRequest) (*base.TargetResponse, base.RPCServerResponseCode, error) {
	if err := applyMatchRulePayers(req.GetMatch(), sc.legacyPayer); err != nil {
		return nil, base.RPCServerResponseCode_INVALID_REQUEST, err
	}

	shard := sc.getGroup()
	cl := shard.getClient()
	res, statusCode, err := cl.doUnary(req, &base.TargetResponse{}, contract.Target)
	if nil != err {
		return nil, statusCode, err
	}
	return res.(*base.TargetResponse), statusCode, nil
}

// Report is used by a third-party platform to report that a specific event has occurred.
// This mechanism is used to record statistical data and perform settlements between system users as part of the third-party billing strategy.
// For more details, see here: [LINK]
//
// req.Payer is the caller's own identity and is required; when empty it falls
// back to the partner id of the deprecated Configuration.JwtToken. Each rule may
// name the client it is billed to in req.Rules[i].Payer; rules that leave it
// empty inherit req.Payer.
func (sc *ShardedClient) Report(req *base.ReportRequest) (base.RPCServerResponseCode, error) {
	if nil == req.TrackingId {
		return base.RPCServerResponseCode_UNKNOWN, fmt.Errorf("tracking id is required")
	}

	if err := sc.applyPayers(req.GetPayer(), func(payer uuid.UUID) error {
		req.Payer = payer.String()
		return applyReportRulePayers(req.GetRules(), payer)
	}); err != nil {
		return base.RPCServerResponseCode_INVALID_REQUEST, err
	}

	// Any open connection will do. The origin node id is encoded in the tracking
	// id, and the cloud forwards a report that lands elsewhere to the node that
	// holds the matching request context - so the client does not need to know
	// which node that is, and must not refuse the report for not knowing.
	_, statusCode, err := sc.getGroup().getClient().doUnary(req, nil, contract.Report)
	return statusCode, err
}

// applyPayers resolves the request-level payer and hands it to stamp, which
// writes it onto the request and onto every rule that names no payer of its own.
//
// The rule sets themselves are not validated here: rule counts, traffic types
// and event counts are the cloud's contract, and duplicating them would only
// change which error a caller sees.
func (sc *ShardedClient) applyPayers(requestPayer string, stamp func(uuid.UUID) error) error {
	payer, err := resolveRequestPayer(requestPayer, sc.legacyPayer)
	if err != nil {
		return err
	}
	return stamp(payer)
}

// applyMatchRulePayers stamps the billed client onto every Target match rule.
//
// defaultPayer may be uuid.Nil: a request with no match rules needs no payer,
// and a request whose every rule names a client needs no default either.
func applyMatchRulePayers(rules []*base.Match_Rule, defaultPayer uuid.UUID) error {
	for i, rule := range rules {
		if rule == nil {
			return fmt.Errorf("match rule %d must not be nil", i)
		}

		resolved, err := resolveRulePayer(rule.GetPayer(), defaultPayer)
		if err != nil {
			return fmt.Errorf("match rule %d: %w", i, err)
		}
		rule.Payer = resolved.String()
	}

	return nil
}

// applyReportRulePayers stamps the billed client onto every report rule.
func applyReportRulePayers(rules []*base.ReportRequest_Rule, requestPayer uuid.UUID) error {
	for i, rule := range rules {
		if rule == nil {
			return fmt.Errorf("report rule %d must not be nil", i)
		}

		resolved, err := resolveRulePayer(rule.GetPayer(), requestPayer)
		if err != nil {
			return fmt.Errorf("report rule %d: %w", i, err)
		}
		rule.Payer = resolved.String()
	}

	return nil
}

// IsValidTrackingID reports whether trackingId is well formed, i.e. whether it
// names an origin node at all.
//
// It no longer says anything about routing: a report is sent over any open
// connection and the cloud forwards it to the origin node. It used to answer
// "does this client hold a connection to the node that owns this tracking id",
// which returned false for perfectly valid tracking ids.
func (sc *ShardedClient) IsValidTrackingID(trackingId []byte) bool {
	return uniqid.GetServerID(trackingId) > 0
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

	sc := &ShardedClient{
		Configuration: *cfg,
		legacyPayer:   payerFromJwtToken(cfg.JwtToken),
	}

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
				rpcRef.onConnDialed()
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
