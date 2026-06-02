package client

import (
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VictoriaMetrics/metrics"
	"github.com/aradilov/fastrpc"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/pkg/contract"
)

func TestNewClientNormalizesAddrs(t *testing.T) {
	tests := []struct {
		name       string
		addrs      string
		expectAddr string
	}{
		{
			name:       "empty addrs use default",
			addrs:      "",
			expectAddr: defaultCloudAddr,
		},
		{
			name:       "whitespace only addrs use default",
			addrs:      " ,  , ",
			expectAddr: defaultCloudAddr,
		},
		{
			name:       "trims and removes empty addrs",
			addrs:      " host-a:1, ,host-b:2 ",
			expectAddr: "host-a:1,host-b:2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(&Configuration{Addrs: tt.addrs}, nil)
			if client.Configuration.Addrs != tt.expectAddr {
				t.Fatalf("expected addrs to be %q, got %q", tt.expectAddr, client.Configuration.Addrs)
			}
		})
	}
}

func TestNewClientDoesNotMutateConfiguration(t *testing.T) {
	cfg := &Configuration{
		Addrs:                          " host-a:1, ,host-b:2 ",
		JwtToken:                       []byte("jwt-token"),
		MaxRequestDuration:             -time.Second,
		MaxDialDuration:                -time.Second,
		MaxPendingRequests:             -1,
		ReadBufferSize:                 -1,
		WriteBufferSize:                -1,
		MaximumSimultaneousConnections: -1,
	}

	client := NewClient(cfg, nil)
	if cfg.Addrs != " host-a:1, ,host-b:2 " {
		t.Fatalf("expected original addrs to remain unchanged, got %q", cfg.Addrs)
	}
	if cfg.MaxRequestDuration != -time.Second {
		t.Fatalf("expected original max request duration to remain unchanged, got %s", cfg.MaxRequestDuration)
	}

	if client.Configuration.Addrs != "host-a:1,host-b:2" {
		t.Fatalf("expected normalized addrs, got %q", client.Configuration.Addrs)
	}
	if client.Configuration.MaxRequestDuration != defaultMaxRequestDuration {
		t.Fatalf("expected default max request duration, got %s", client.Configuration.MaxRequestDuration)
	}
	if client.Configuration.MaxDialDuration != defaultMaxRequestDuration {
		t.Fatalf("expected default max dial duration, got %s", client.Configuration.MaxDialDuration)
	}
	if client.Configuration.DNSRefreshInterval != defaultDNSRefreshInterval {
		t.Fatalf("expected default DNS refresh interval, got %s", client.Configuration.DNSRefreshInterval)
	}
	if client.Configuration.MaxPendingRequests != defaultMaxPendingRequests {
		t.Fatalf("expected default max pending requests, got %d", client.Configuration.MaxPendingRequests)
	}
	if client.Configuration.ReadBufferSize != defaultBufferSize {
		t.Fatalf("expected default read buffer size, got %d", client.Configuration.ReadBufferSize)
	}
	if client.Configuration.WriteBufferSize != defaultBufferSize {
		t.Fatalf("expected default write buffer size, got %d", client.Configuration.WriteBufferSize)
	}
}

func TestNewClientSupportsDeprecatedJvtToken(t *testing.T) {
	client := NewClient(&Configuration{Addrs: "host-a:1", JvtToken: []byte("legacy-token")}, nil)
	if string(client.Configuration.JwtToken) != "legacy-token" {
		t.Fatalf("expected JwtToken to use deprecated JvtToken fallback, got %q", client.Configuration.JwtToken)
	}
	if string(client.clients[0].clients[0].JwtToken) != "legacy-token" {
		t.Fatalf("expected internal client to use deprecated JvtToken fallback, got %q", client.clients[0].clients[0].JwtToken)
	}
}

func TestBuildShardMetricsUsesRequestAndAddrLabels(t *testing.T) {
	shardAddr := "metrics-labels.local:7943"
	groups := buildShardMetrics(shardAddr)

	want := metrics.GetOrCreateCounter(`dcrRPCClientRequest{request="target",addr="metrics-labels.local:7943"}`)
	if groups[contract.Target].request != want {
		t.Fatalf("expected target request counter to use request and addr labels")
	}
}

func TestDoUnaryIncrementsRequestMetric(t *testing.T) {
	metricGroups := buildShardMetrics("request-counter.local:7943")
	requests := metricGroups[contract.Mock].request
	before := requests.Get()

	cl := &client{
		maxRequestDuration: time.Millisecond,
		metricGroups:       metricGroups,
		c: &fastrpc.Client{
			Addr: "127.0.0.1:1",
			NewResponse: func() fastrpc.ResponseReader {
				return &contract.Response{}
			},
			Dial: func(addr string) (net.Conn, error) {
				return nil, errors.New("dial failed")
			},
		},
	}
	cl.connGen.Store(1)
	atomic.StoreUint64(&cl.authedGen, 1)

	_, _, _ = cl.doUnary(&base.MockRequest{StatusCode: base.RPCServerResponseCode_UNAUTHORIZED}, nil, contract.Mock)
	if got := requests.Get(); got != before+1 {
		t.Fatalf("expected request counter to increment to %d, got %d", before+1, got)
	}
}
