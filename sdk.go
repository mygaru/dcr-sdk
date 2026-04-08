package dcr_sdk

import (
	"crypto/tls"
	"github.com/mygaru/dcr-sdk/pkg/client"
)

// NewWithTLS creates an RPC client that can communicate with the DCR cloud
// TLS configuration is mandatory and is based on mTLS
// The key can be obtained after contacting the manager or downloaded directly from the profile section of the member zone.
func NewWithTLS(cfg *client.Configuration, tls *tls.Config) *client.ShardedClient {
	if nil == tls {
		panic("tls config is required")
	}
	return client.NewClient(cfg, tls)
}

// New creates a test RPC Client that can communicate with a test RPC server
// For example dcrMockServer
// The New method is used in tests and debug cases, for a real connection to the DCR cloud use NewWithTLS
func New(cfg *client.Configuration) *client.ShardedClient {
	if cfg == nil {
		panic("configuration is required; RPC addr of test server must be specified in the configuration")
	}
	return client.NewClient(cfg, nil)
}
