package dcr_sdk

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"github.com/mygaru/dcr-sdk/pkg/client"
	"os"
)

// LoadTLSConfig builds tls.Config from certificate files.
// certFile - client/server certificate in PEM
// keyFile  - private key in PEM
// caFile   - CA certificate in PEM (optional if empty)
func LoadTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
	// Load leaf certificate + private key
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load x509 key pair: %w", err)
	}

	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	// Load CA if provided
	if caFile != "" {
		caPEM, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read ca file: %w", err)
		}

		caPool := x509.NewCertPool()
		if !caPool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("append CA certs from PEM: invalid PEM data")
		}

		cfg.RootCAs = caPool   // for client side
		cfg.ClientCAs = caPool // for server side mutual TLS
		cfg.ClientAuth = tls.NoClientCert
	}

	return cfg, nil
}

func New(cfg *client.Configuration) *client.ShardedClient {
	if nil == cfg {
		cfg = &client.Configuration{}
	}

	if cfg.Addrs == "" {
		cfg.Addrs = "cloud.mygaru.com:8080"
	}

	return client.NewClient(cfg, nil)
}
