package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/mygaru/dcr-sdk/internal/testcloud"
	"github.com/mygaru/dcr-sdk/pkg/serverauth"
)

var (
	listenAddr   = flag.String("listenAddr", "127.0.0.1:7943", "TCP address for accepting test-cloud RPC requests")
	serverID     = flag.Uint("serverID", 1024, "server id encoded into generated tracking ids")
	tlsCertPath  = flag.String("tlsCert", "", "PEM-encoded TLS server certificate")
	tlsKeyPath   = flag.String("tlsKey", "", "PEM-encoded TLS server private key")
	clientCAPath = flag.String("clientCA", "", "PEM-encoded CA used to verify mTLS client certificates")
	clientIssuer = flag.String("clientIssuer", "", "PEM-encoded issuer certificate used for OCSP checks")
	requireOCSP  = flag.Bool("requireOCSP", false, "require good OCSP status for mTLS client certificates")
)

func main() {
	flag.Parse()

	tlsConfig, err := loadTLSConfig()
	if err != nil {
		log.Fatalf("test-cloud: load TLS config: %v", err)
	}

	log.Printf("Starting test-cloud RPC server at %q", *listenAddr)
	err = testcloud.ListenAndServe(testcloud.Config{
		ListenAddr: *listenAddr,
		ServerID:   uint16(*serverID),
		TLSConfig:  tlsConfig,
	})
	if err != nil {
		log.Fatalf("test-cloud: serve failed on %q: %v", *listenAddr, err)
	}
}

func loadTLSConfig() (*tls.Config, error) {
	if *tlsCertPath == "" && *tlsKeyPath == "" && *clientCAPath == "" {
		return nil, nil
	}
	if *tlsCertPath == "" || *tlsKeyPath == "" || *clientCAPath == "" {
		return nil, fmt.Errorf("tlsCert, tlsKey, and clientCA must be set together")
	}

	serverCert, err := tls.LoadX509KeyPair(*tlsCertPath, *tlsKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load server key pair: %w", err)
	}

	clientCAPEM, err := os.ReadFile(*clientCAPath)
	if err != nil {
		return nil, fmt.Errorf("read client CA: %w", err)
	}
	clientRoots := x509.NewCertPool()
	if !clientRoots.AppendCertsFromPEM(clientCAPEM) {
		return nil, fmt.Errorf("parse client CA")
	}

	var issuer *x509.Certificate
	if *clientIssuer != "" {
		issuer, err = readCertificate(*clientIssuer)
		if err != nil {
			return nil, fmt.Errorf("read client issuer: %w", err)
		}
	}

	// Example for real mTLS auth failure handling:
	// if unauthorized {
	// 	return nil, fmt.Errorf("unauthorized")
	// }
	// fastrpc closes the connection when the TLS handshake returns an error.

	return serverauth.NewTLSConfig(&tls.Config{
		Certificates: []tls.Certificate{serverCert},
	}, serverauth.MTLSConfig{
		Roots:       clientRoots,
		Issuer:      issuer,
		RequireOCSP: *requireOCSP,
	}), nil
}

func readCertificate(path string) (*x509.Certificate, error) {
	certPEM, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("decode certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}
