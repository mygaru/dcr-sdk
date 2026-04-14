package dcr_sdk

import (
	"testing"

	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/internal/dcrMockServer"

	"github.com/mygaru/dcr-sdk/pkg/client"
)

func init() {
	dcrMockServer.Init()
}

const MaximumSimultaneousConnections = 4

func getTestClient() *client.ShardedClient {
	return New(&client.Configuration{Addrs: *dcrMockServer.ListenAddr, JvtToken: []byte("some token here ..."), MaximumSimultaneousConnections: MaximumSimultaneousConnections})
}

func TestAuth(t *testing.T) {
	rpc := getTestClient()
	for i := 0; i < 100; i++ {
		_, sc, err := rpc.Mock(&base.MockRequest{StatusCode: base.RPCServerResponseCode_OK}, nil)
		if nil != err {
			t.Fatalf("expected nil, got err %v", err)
		}

		if sc != base.RPCServerResponseCode_OK {
			t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_OK, sc)
		}
	}

	if rpc.Reconnects() != MaximumSimultaneousConnections {
		t.Fatalf("expected %d reconnects, got %d", MaximumSimultaneousConnections, rpc.Reconnects())
	}
}

func TestFailed(t *testing.T) {
	_, sc, err := getTestClient().Mock(&base.MockRequest{StatusCode: base.RPCServerResponseCode_UNAUTHORIZED}, nil)
	if nil == err {
		t.Fatalf("expected error, got none")
	}

	if sc != base.RPCServerResponseCode_UNAUTHORIZED {
		t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_UNAUTHORIZED, sc)
	}
}

func TestNew(t *testing.T) {

	tests := []struct {
		name       string
		cfg        *client.Configuration
		expectAddr string
	}{
		{
			name:       "nil configuration",
			cfg:        &client.Configuration{},
			expectAddr: "cloud.mygaru.com:7943",
		},
		{
			name: "addr configuration",
			cfg: &client.Configuration{
				Addrs: "anyaddr.here:8080",
			},
			expectAddr: "anyaddr.here:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := New(tt.cfg)
			if client.Configuration.Addrs != tt.expectAddr {
				t.Errorf("expected addrs to be %q, got %q", tt.expectAddr, client.Configuration.Addrs)
			}
		})
	}
}
