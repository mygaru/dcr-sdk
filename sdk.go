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

// CertProvider serves a client's certificate and renews it; see
// client.CertProvider. Get a client's with ShardedClient.CertProvider.
type CertProvider = client.CertProvider

// SaveToFiles is an MTLSConfig.OnRenewed that saves a renewed certificate
// and key over two PEM files; see client.SaveToFiles.
func SaveToFiles(certPath, keyPath string) func(certPEM, keyPEM []byte) error {
	return client.SaveToFiles(certPath, keyPath)
}

// DefaultCAURL is the myGaru CA that issues and renews client certificates.
const DefaultCAURL = client.DefaultCAURL

// NewWithMTLS creates a client of the DCR cloud that authenticates with a client
// certificate; see client.NewWithMTLS.
func NewWithMTLS(cfg *client.Configuration, mtlsCfg MTLSConfig) (*client.ShardedClient, error) {
	return client.NewWithMTLS(cfg, mtlsCfg)
}
