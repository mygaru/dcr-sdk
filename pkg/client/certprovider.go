package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// DefaultCAURL is the myGaru CA that issues and renews client certificates.
const DefaultCAURL = "https://ca.mygaru.com"

// caRenewPath is where the CA renews the certificate a request is made with.
const caRenewPath = "/cert"

const (
	// defaultCAPollInterval is how long Fetch waits before asking again when
	// the CA has accepted a renewal but not issued it yet.
	defaultCAPollInterval = 20 * time.Second

	// caResponseLimit bounds what Fetch reads of a CA response.
	caResponseLimit = 1 << 20
)

// CertProvider holds the client certificate of a ShardedClient and serves it to
// every new connection, so a renewed certificate is picked up without
// recreating the client. Get it with ShardedClient.CertProvider.
//
// A renewal is the application's to drive, typically from a background worker:
//
//	leaf := provider.Leaf()
//	if time.Until(leaf.NotAfter) < leaf.NotAfter.Sub(leaf.NotBefore)/10 {
//		certPEM, keyPEM, err := provider.Fetch(ctx, "")
//		// save certPEM and keyPEM where the application loads them from, then
//		err = provider.Update(certPEM, keyPEM)
//		cli.Reconnect(false)
//	}
//
// It is safe for concurrent use.
type CertProvider struct {
	check   certCheck
	current atomic.Pointer[providedCert]

	// caRoots verifies the CA's server certificate; nil for the system roots,
	// which the CA's (ACME) certificate is issued under. Set by tests.
	caRoots *x509.CertPool
	// pollInterval overrides defaultCAPollInterval; set by tests.
	pollInterval time.Duration
}

// providedCert is one certificate with the PEM it was loaded from.
type providedCert struct {
	cert    tls.Certificate
	certPEM []byte
}

func newCertProvider(cfg MTLSConfig) (*CertProvider, error) {
	p := &CertProvider{check: newCertCheck(cfg)}
	if err := p.Update(cfg.CertPEM, cfg.KeyPEM); err != nil {
		return nil, err
	}
	return p, nil
}

// GetClientCertificate returns the current certificate. It is the
// tls.Config.GetClientCertificate of the client's connections.
func (p *CertProvider) GetClientCertificate(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	return &p.current.Load().cert, nil
}

// Leaf is the current certificate, parsed: its NotAfter and NotBefore say when
// to renew it, its SerialNumber whether a fetched one is new.
func (p *CertProvider) Leaf() *x509.Certificate {
	return p.current.Load().cert.Leaf
}

// CertPEM is the PEM the current certificate was loaded from.
func (p *CertProvider) CertPEM() []byte {
	return bytes.Clone(p.current.Load().certPEM)
}

// Update replaces the certificate with certPEM and keyPEM, after validating
// them as NewWithMTLS validates the first one: a pair that does not load or a
// certificate the cloud would refuse leaves the current one in place.
//
// New connections present the new certificate; open ones keep the one they
// were established with until Reconnect, or until they are re-dialed anyway.
func (p *CertProvider) Update(certPEM, keyPEM []byte) error {
	cert, err := p.check.load(certPEM, keyPEM)
	if err != nil {
		return err
	}
	p.current.Store(&providedCert{cert: cert, certPEM: bytes.Clone(certPEM)})
	return nil
}

// caRenewResponse is the CA's answer to a renewal that has been issued.
type caRenewResponse struct {
	CommonName string `json:"COMMON_NAME"`
	Serial     string `json:"SERIAL"`
	// Chain is the new certificate followed by its intermediates.
	Chain string `json:"CHAIN"`
	// Key is the private key of the new certificate: the CA generates it.
	Key    string `json:"Key"`
	Status string `json:"Status"`
	Reason string `json:"reason"`
}

// Fetch renews the certificate at the CA and returns the new certificate and
// its key. It changes nothing: hand the result to Update, after saving it
// wherever the application loads its certificate from.
//
// caURL is the CA's base URL, DefaultCAURL when empty; the renewal is asked of
// its /cert endpoint, over mTLS with the current certificate. The CA may take
// a while to issue it, during which Fetch asks again every 20 seconds, so ctx
// should carry a deadline: a minute or two is plenty.
//
// The CA may answer with the certificate already held - compare the serial
// numbers before treating the result as new.
func (p *CertProvider) Fetch(ctx context.Context, caURL string) (certPEM, keyPEM []byte, err error) {
	if caURL == "" {
		caURL = DefaultCAURL
	}
	url := strings.TrimRight(caURL, "/") + caRenewPath

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				GetClientCertificate: p.GetClientCertificate,
				RootCAs:              p.caRoots,
				MinVersion:           tls.VersionTLS12,
			},
			// a new connection per renewal: renewals are hours apart
			DisableKeepAlives: true,
		},
	}

	pollInterval := p.pollInterval
	if pollInterval <= 0 {
		pollInterval = defaultCAPollInterval
	}

	// accepted is whether the CA has taken the renewal on: a deadline after
	// that is the CA being slow, not unreachable
	accepted := false

	for {
		status, body, err := p.askCA(ctx, httpClient, url)
		if err != nil {
			if accepted && ctx.Err() != nil {
				return nil, nil, fmt.Errorf("renew certificate at %s: CA has not issued it yet: %w", url, ctx.Err())
			}
			return nil, nil, err
		}

		switch status {
		case http.StatusOK:
			return parseCARenewResponse(body)

		case http.StatusAccepted:
			// accepted, not issued yet
			accepted = true
			select {
			case <-ctx.Done():
				return nil, nil, fmt.Errorf("renew certificate at %s: CA has not issued it yet: %w", url, ctx.Err())
			case <-time.After(pollInterval):
			}

		default:
			return nil, nil, fmt.Errorf("renew certificate at %s: CA answered %d: %s", url, status, bytes.TrimSpace(body))
		}
	}
}

func (p *CertProvider) askCA(ctx context.Context, httpClient *http.Client, url string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(`{}`))
	if err != nil {
		return 0, nil, fmt.Errorf("renew certificate at %s: %w", url, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("renew certificate at %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, caResponseLimit))
	if err != nil {
		return 0, nil, fmt.Errorf("renew certificate at %s: read response: %w", url, err)
	}
	return resp.StatusCode, body, nil
}

func parseCARenewResponse(body []byte) (certPEM, keyPEM []byte, err error) {
	var r caRenewResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, nil, fmt.Errorf("decode CA response: %w", err)
	}
	if r.Chain == "" || r.Key == "" || r.Chain == "null" || r.Key == "null" {
		return nil, nil, fmt.Errorf("CA issued no certificate (status=%q reason=%q)", r.Status, r.Reason)
	}

	certPEM, keyPEM = []byte(r.Chain), []byte(r.Key)
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return nil, nil, fmt.Errorf("CA issued an unusable certificate: %w", err)
	}
	return certPEM, keyPEM, nil
}
