// Package dcr_sdk is the client of the myGaru DCR cloud.
//
// Create a client with NewWithMTLS: the cloud accepts mTLS connections only.
package dcr_sdk

import (
	"github.com/mygaru/dcr-sdk/pkg/client"
)

// MTLSConfig contains the certificate material and validation settings of an
// mTLS client; see client.MTLSConfig.
type MTLSConfig = client.MTLSConfig

// NewWithMTLS creates a client of the DCR cloud that authenticates with a client
// certificate; see client.NewWithMTLS.
func NewWithMTLS(cfg *client.Configuration, mtlsCfg MTLSConfig) (*client.ShardedClient, error) {
	return client.NewWithMTLS(cfg, mtlsCfg)
}
