package dcr_sdk

import (
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/internal/dcrMockServer"
	"testing"

	"github.com/mygaru/dcr-sdk/pkg/client"
)

func init() {
	dcrMockServer.Init()
}

func getTestClient() *client.ShardedClient {
	return New(&client.Configuration{Addrs: *dcrMockServer.ListenAddr})
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
