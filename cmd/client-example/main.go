package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"time"

	dcr "github.com/mygaru/dcr-sdk-pub"
	base "github.com/mygaru/dcr-sdk-pub/gen/base1"
	"github.com/mygaru/dcr-sdk-pub/pkg/client"
)

//
// @andrey - why do we ever need server CA roots?
// We're using well-known ACME CAs for servers, so roots should be available in OS's trust store
//

type Config struct {
	CertPath string // The path to the client certificate file
	KeyPath  string // The path to the client key file
	CAPath   string // @andrey, see question above
	CAURL    string // The URL for CA to fetch certificates
	DCRAddr  string // The comma-separated list of DCR servers
	ServerID string // The server name for TLS SNI
}

func main() {
	cfg := Config{
		CertPath: "./certs/client.pem",
		KeyPath:  "./certs/client-key.pem",
		CAPath:   "./certs/server-ca.pem",
		CAURL:    "https://ca.mygaru.com/cert",
		DCRAddr:  "cloud.mygaru.com:7937",
		ServerID: "cloud.mygaru.com",
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Step 1: App loads initial certificate / key from files
	certPEM, keyPEM, serverRoots, err := loadCertsFromDisk(cfg)
	if err != nil {
		log.Fatalf("failed to load initial certificates: %v", err)
	}

	// Step 2: App initializes SDK, using loaded certificates
	// CertProvider manages in-memory cert state and provides GetClientCertificate
	certProvider, err := dcr.NewCertProvider(dcr.MTLSConfig{
		CertPEM:       certPEM,
		KeyPEM:        keyPEM,
		ServerRootCAs: serverRoots, // @andrey, see question above
	})
	if err != nil {
		log.Fatalf("failed to initialize CertProvider: %v", err)
	}

	tlsConfig := &tls.Config{
		GetClientCertificate: certProvider.GetClientCertificate,
		RootCAs:              serverRoots, // @andrey, see question above
		ServerName:           cfg.ServerID,
		MinVersion:           tls.VersionTLS12,
	}

	sdkClient := dcr.NewWithTLS(&client.Configuration{
		Addrs:                          cfg.DCRAddr,
		MaxRequestDuration:             time.Second,
		MaximumSimultaneousConnections: 128,
	}, tlsConfig)

	// Step 3: App runs background thread for cert expiration monitoring & auto-renewal
	go runCertAutoRenewer(ctx, certProvider, sdkClient, cfg)

	// Perform normal application operations
	// ...

}

// runCertAutoRenewer is the background thread monitoring certificate expiration.
func runCertAutoRenewer(ctx context.Context, provider *dcr.CertProvider, sdkClient *client.ShardedClient, cfg Config) {
	// Check every 12 hours
	ticker := time.NewTicker(12 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := checkAndRenewCert(ctx, provider, sdkClient, cfg); err != nil {
				log.Printf("[CertRenewer] Warning: renewal check failed: %v", err)
			}
		}
	}
}

func checkAndRenewCert(ctx context.Context, provider *dcr.CertProvider, sdkClient *client.ShardedClient, cfg Config) error {
	leaf := provider.Leaf()
	if leaf == nil {
		return fmt.Errorf("certificate leaf is unavailable")
	}

	totalLifetime := leaf.NotAfter.Sub(leaf.NotBefore)
	timeLeft := time.Until(leaf.NotAfter)

	// Condition: time left to expiration is less than 10% of total certificate lifetime
	threshold := time.Duration(float64(totalLifetime) * 0.10)
	if timeLeft > threshold {
		log.Printf("[CertRenewer] Cert is valid (%v left). Renewal threshold (%v) not reached.", timeLeft.Round(time.Hour), threshold.Round(time.Hour))
		return nil
	}

	log.Printf("[CertRenewer] Cert expiration near (%v left < 10%% threshold %v). Fetching new cert from CA...", timeLeft, threshold)

	// a) Call certProvider.Fetch to get new cert from CA via mTLS
	newCertPEM, newKeyPEM, err := provider.Fetch(ctx, cfg.CAURL)
	if err != nil {
		return fmt.Errorf("Fetch failed: %w", err)
	}

	// Verify if certificate is actually new
	if bytes.Equal(provider.CertPEM(), newCertPEM) {
		log.Printf("[CertRenewer] CA returned identical certificate. No update needed.")
		return nil
	}

	// b) Save received certificate / key to local files atomically
	if err := saveFileAtomic(cfg.CertPath, newCertPEM, 0644); err != nil {
		return fmt.Errorf("failed to save cert file: %w", err)
	}
	if err := saveFileAtomic(cfg.KeyPath, newKeyPEM, 0600); err != nil {
		return fmt.Errorf("failed to save key file: %w", err)
	}
	log.Printf("[CertRenewer] Saved new cert/key files to disk (%s, %s)", cfg.CertPath, cfg.KeyPath)

	// c) Call certProvider.Update to update certificate in SDK runtime
	if err := provider.Update(newCertPEM, newKeyPEM); err != nil {
		return fmt.Errorf("runtime update failed: %w", err)
	}
	log.Printf("[CertRenewer] Certificate successfully updated in SDK runtime")

	// d) (Optionally) gracefully restart existing connections
	if sdkClient != nil {
		sdkClient.Reconnect(false) // false = graceful reconnect
		log.Printf("[CertRenewer] Gracefully restarted active SDK connections")
	}

	return nil
}

func loadCertsFromDisk(cfg Config) ([]byte, []byte, *x509.CertPool, error) {
	return nil, nil, nil, fmt.Errorf("DIY")
}

func saveFileAtomic(filename string, data []byte, perm os.FileMode) error {
	// write temporary file and then os.Rename it to the target filename
	return fmt.Errorf("DIY")
}
