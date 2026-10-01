package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/internal/testcloud"
)

func testMTLSConfigFor(certs *testcloud.Certificates, certPEM, keyPEM []byte) MTLSConfig {
	return MTLSConfig{
		CertPEM:         certPEM,
		KeyPEM:          keyPEM,
		ServerRootCAs:   certs.ServerRoots,
		ServerName:      certs.ServerName,
		ClientCertRoots: certs.ClientRoots,
	}
}

func newTestCertificates(t *testing.T) *testcloud.Certificates {
	t.Helper()
	certs, err := testcloud.NewCertificates()
	if err != nil {
		t.Fatalf("generate test certificates: %v", err)
	}
	return certs
}

func issue(t *testing.T, certs *testcloud.Certificates, id uuid.UUID) (certPEM, keyPEM []byte) {
	t.Helper()
	certPEM, keyPEM, err := certs.NewClientCertificate(id)
	if err != nil {
		t.Fatalf("issue certificate: %v", err)
	}
	return certPEM, keyPEM
}

func TestCertProviderUpdate(t *testing.T) {
	certs := newTestCertificates(t)
	sc, err := NewWithMTLS(&Configuration{Addrs: "127.0.0.1:1"}, testMTLSConfigFor(certs, certs.ClientCertPEM, certs.ClientKeyPEM))
	if err != nil {
		t.Fatalf("NewWithMTLS: %v", err)
	}
	p := sc.CertProvider()
	first := p.Leaf().SerialNumber

	renewedID := uuid.New()
	renewedCert, renewedKey := issue(t, certs, renewedID)
	if err := p.Update(renewedCert, renewedKey); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if p.Leaf().SerialNumber.Cmp(first) == 0 {
		t.Fatal("Update kept the old certificate")
	}
	if got, _ := p.GetClientCertificate(nil); got.Leaf.SerialNumber.Cmp(p.Leaf().SerialNumber) != 0 {
		t.Fatal("GetClientCertificate does not serve the updated certificate")
	}
	if string(p.CertPEM()) != string(renewedCert) {
		t.Fatal("CertPEM is not the updated certificate")
	}

	// what does not validate leaves the current certificate in place
	other := newTestCertificates(t)
	otherCert, otherKey := issue(t, other, uuid.New())
	for name, pair := range map[string][2][]byte{
		"mismatched key":   {renewedCert, certs.ClientKeyPEM},
		"untrusted CA":     {otherCert, otherKey},
		"not a pem at all": {[]byte("nope"), []byte("nope")},
	} {
		if err := p.Update(pair[0], pair[1]); err == nil {
			t.Errorf("Update(%s) = nil, want an error", name)
		}
	}
	if string(p.CertPEM()) != string(renewedCert) {
		t.Fatal("a refused Update replaced the certificate")
	}
}

// fakeCA answers renewals the way the myGaru CA does: 202 until it has issued
// the certificate, then 200 with it.
type fakeCA struct {
	mu       sync.Mutex
	pending  int // 202s left before the 200
	status   int // answered instead of 200 when not zero
	issued   [2][]byte
	asked    []string
	callerCN []string
}

func (ca *fakeCA) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	ca.asked = append(ca.asked, r.Method+" "+r.URL.Path)
	if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
		ca.callerCN = append(ca.callerCN, r.TLS.PeerCertificates[0].Subject.CommonName)
	}

	switch {
	case ca.status != 0:
		w.WriteHeader(ca.status)
		_, _ = w.Write([]byte("certificate revoked"))
	case ca.pending > 0:
		ca.pending--
		w.WriteHeader(http.StatusAccepted)
	default:
		_ = json.NewEncoder(w).Encode(caRenewResponse{
			CommonName: "dcr-sdk-client",
			Serial:     "1",
			Chain:      string(ca.issued[0]),
			Key:        string(ca.issued[1]),
			Status:     "good",
		})
	}
}

func startFakeCA(t *testing.T, ca *fakeCA) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(ca)
	server.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func newTestProvider(t *testing.T, certs *testcloud.Certificates, ca *httptest.Server) *CertProvider {
	t.Helper()
	sc, err := NewWithMTLS(&Configuration{Addrs: "127.0.0.1:1"}, testMTLSConfigFor(certs, certs.ClientCertPEM, certs.ClientKeyPEM))
	if err != nil {
		t.Fatalf("NewWithMTLS: %v", err)
	}
	p := sc.CertProvider()
	p.caRoots = x509.NewCertPool()
	p.caRoots.AddCert(ca.Certificate())
	p.pollInterval = 10 * time.Millisecond
	return p
}

func TestFetchRenewsAtTheCA(t *testing.T) {
	certs := newTestCertificates(t)
	issuedCert, issuedKey := issue(t, certs, uuid.New())
	ca := &fakeCA{pending: 2, issued: [2][]byte{issuedCert, issuedKey}}
	server := startFakeCA(t, ca)
	p := newTestProvider(t, certs, server)
	held := p.CertPEM()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// a trailing slash on the base URL changes nothing
	certPEM, keyPEM, err := p.Fetch(ctx, server.URL+"/")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(certPEM) != string(issuedCert) || string(keyPEM) != string(issuedKey) {
		t.Fatal("Fetch did not return what the CA issued")
	}
	if string(p.CertPEM()) != string(held) {
		t.Fatal("Fetch changed the certificate in use; that is Update's job")
	}

	ca.mu.Lock()
	defer ca.mu.Unlock()
	if want := []string{"POST /cert", "POST /cert", "POST /cert"}; strings.Join(ca.asked, ",") != strings.Join(want, ",") {
		t.Fatalf("CA was asked %q, want %q (two 202s, then the 200)", ca.asked, want)
	}
	for _, cn := range ca.callerCN {
		if cn != "dcr-sdk-client" {
			t.Fatalf("CA saw client certificate %q, want the current one", cn)
		}
	}
}

func TestFetchFailures(t *testing.T) {
	certs := newTestCertificates(t)

	t.Run("CA refuses", func(t *testing.T) {
		server := startFakeCA(t, &fakeCA{status: http.StatusForbidden})
		p := newTestProvider(t, certs, server)

		_, _, err := p.Fetch(context.Background(), server.URL)
		if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "certificate revoked") {
			t.Fatalf("Fetch = %v; want the CA's 403 and its reason", err)
		}
	})

	t.Run("CA never issues", func(t *testing.T) {
		server := startFakeCA(t, &fakeCA{pending: 1 << 30})
		p := newTestProvider(t, certs, server)

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_, _, err := p.Fetch(ctx, server.URL)
		if err == nil || !strings.Contains(err.Error(), "not issued it yet") {
			t.Fatalf("Fetch = %v; want a deadline while waiting for the CA", err)
		}
	})

	t.Run("CA issues nothing", func(t *testing.T) {
		server := startFakeCA(t, &fakeCA{})
		p := newTestProvider(t, certs, server)

		if _, _, err := p.Fetch(context.Background(), server.URL); err == nil {
			t.Fatal("Fetch accepted an empty certificate")
		}
	})
}

func TestFetchDefaultsToTheMyGaruCA(t *testing.T) {
	if got := strings.TrimRight(DefaultCAURL, "/") + caRenewPath; got != "https://ca.mygaru.com/cert" {
		t.Fatalf("default renewal URL = %s", got)
	}
}

// TestReconnectMovesConnectionsToTheNewCertificate: after Update the open
// connection keeps the old certificate; Reconnect gets it onto the new one.
func TestReconnectMovesConnectionsToTheNewCertificate(t *testing.T) {
	for _, force := range []bool{false, true} {
		name := "graceful"
		if force {
			name = "force"
		}
		t.Run(name, func(t *testing.T) {
			certs := newTestCertificates(t)
			server, err := testcloud.Start(testcloud.Config{TLSConfig: certs.ServerTLSConfig})
			if err != nil {
				t.Fatalf("start test-cloud: %v", err)
			}
			t.Cleanup(func() { _ = server.Close() })

			sc, err := NewWithMTLS(&Configuration{Addrs: server.Addr(), MaximumSimultaneousConnections: 1},
				testMTLSConfigFor(certs, certs.ClientCertPEM, certs.ClientKeyPEM))
			if err != nil {
				t.Fatalf("NewWithMTLS: %v", err)
			}

			target := func() uuid.UUID {
				t.Helper()
				if _, sc, err := sc.Target(&base.TargetRequest{
					Uids: []*base.UID{{Id: []byte("device-id"), Type: base.UID_DEVICE_ID}},
				}); err != nil {
					t.Fatalf("Target = %s, %v", sc, err)
				}
				return server.LastCaller()
			}

			if got := target(); got != certs.ClientID {
				t.Fatalf("first Target came as %s, want %s", got, certs.ClientID)
			}

			renewedID := uuid.New()
			renewedCert, renewedKey := issue(t, certs, renewedID)
			if err := sc.CertProvider().Update(renewedCert, renewedKey); err != nil {
				t.Fatalf("Update: %v", err)
			}
			if got := target(); got != certs.ClientID {
				t.Fatalf("Target before Reconnect came as %s, want the open connection's %s", got, certs.ClientID)
			}

			sc.Reconnect(force)
			if got := target(); got != renewedID {
				t.Fatalf("Target after Reconnect came as %s, want the renewed %s", got, renewedID)
			}
		})
	}
}
