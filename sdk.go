package dcr_sdk

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"time"

	"github.com/mygaru/dcr-sdk/pkg/client"
	"gitlab.adtelligent.com/awesome/mtls"
)

// MTLSConfig contains certificate material and validation settings for an mTLS client.
type MTLSConfig struct {
	// CertPEM is the PEM-encoded client certificate. It may include intermediate certificates.
	CertPEM []byte
	// KeyPEM is the PEM-encoded private key for CertPEM.
	KeyPEM []byte
	// ServerRootCAs is the CA pool used to verify the RPC server certificate.
	ServerRootCAs *x509.CertPool
	// ServerName is used for RPC server certificate hostname verification.
	ServerName string
	// MinVersion optionally overrides the minimum TLS version. When zero, TLS 1.2 is used.
	MinVersion uint16
	// ClientCertRoots is the CA pool used by mtls.CheckTLS to validate CertPEM.
	// When nil, mtls.CheckTLS uses the system root CA pool.
	ClientCertRoots *x509.CertPool
	// ClientCertIntermediates is an optional intermediate CA pool used by mtls.CheckTLS.
	ClientCertIntermediates *x509.CertPool
	// CurrentTime optionally overrides certificate validity checks. When zero, the current time is used.
	CurrentTime time.Time
}

// NewWithTLS creates an RPC client that can communicate with the DCR cloud
// TLS configuration is mandatory and is based on mTLS
// The key can be obtained after contacting the manager or downloaded directly from the profile section of the member zone.
func NewWithTLS(cfg *client.Configuration, tls *tls.Config) *client.ShardedClient {
	if nil == tls {
		panic("tls config is required")
	}
	return client.NewClient(cfg, tls)
}

// NewWithMTLS creates an RPC client from PEM-encoded mTLS certificate material.
func NewWithMTLS(cfg *client.Configuration, mtlsCfg MTLSConfig) (*client.ShardedClient, error) {
	tlsConfig, err := NewMTLSClientConfig(mtlsCfg)
	if err != nil {
		return nil, err
	}
	return NewWithTLS(cfg, tlsConfig), nil
}

// NewMTLSClientConfig builds a TLS client config and validates the client certificate with mtls.CheckTLS.
func NewMTLSClientConfig(cfg MTLSConfig) (*tls.Config, error) {
	cert, err := tls.X509KeyPair(cfg.CertPEM, cfg.KeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse mTLS client certificate: %w", err)
	}
	if len(cert.Certificate) == 0 {
		return nil, fmt.Errorf("mTLS client certificate is empty")
	}

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse mTLS leaf certificate: %w", err)
	}
	cert.Leaf = leaf

	intermediates := cfg.ClientCertIntermediates
	if len(cert.Certificate) > 1 {
		if intermediates == nil {
			intermediates = x509.NewCertPool()
		} else {
			intermediates = intermediates.Clone()
		}
		for _, certDER := range cert.Certificate[1:] {
			intermediate, err := x509.ParseCertificate(certDER)
			if err != nil {
				return nil, fmt.Errorf("parse mTLS intermediate certificate: %w", err)
			}
			intermediates.AddCert(intermediate)
		}
	}

	if err := mtls.CheckTLS(leaf, mtls.CheckTLSConfig{
		Roots:         cfg.ClientCertRoots,
		Intermediates: intermediates,
		CurrentTime:   cfg.CurrentTime,
	}); err != nil {
		return nil, fmt.Errorf("validate mTLS client certificate: %w", err)
	}

	minVersion := cfg.MinVersion
	if minVersion == 0 {
		minVersion = tls.VersionTLS12
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      cfg.ServerRootCAs,
		ServerName:   cfg.ServerName,
		MinVersion:   minVersion,
	}, nil
}

// New creates a test RPC client that can communicate with a non-TLS RPC server.
// The New method is used in tests and debug cases, for a real connection to the DCR cloud use NewWithTLS
func New(cfg *client.Configuration) *client.ShardedClient {
	if cfg == nil {
		panic("configuration is required; RPC addr of test server must be specified in the configuration")
	}
	return client.NewClient(cfg, nil)
}
