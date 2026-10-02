package client

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/internal/testcloud"
)

func TestResolveServerName(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		addrs      string
		want       string
		wantErr    string
	}{
		// The regression: a caller who never set ServerName used to get a client
		// whose every TLS handshake failed, reported only as a request timeout.
		{"inferred from the address", "", "cloud.mygaru.com:7937", "cloud.mygaru.com", ""},
		{"explicit wins", "other.example.com", "cloud.mygaru.com:7937", "other.example.com", ""},
		{"default address", "", "", "cloud.mygaru.com", ""},
		{"same host, several ports", "", "a.example.com:1,a.example.com:2", "a.example.com", ""},
		{"spaces are trimmed", "", " cloud.mygaru.com:7937 ", "cloud.mygaru.com", ""},
		// An IP has no name to verify, but Go checks it against the certificate's
		// IP SANs, which is the same thing tls.Dial would do.
		{"literal ip", "", "10.0.0.1:7937", "10.0.0.1", ""},

		// The one case the caller has to settle: nothing can stand for both.
		{"different hosts", "", "a.example.com:1,b.example.com:1", "", "more than one host"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveServerName(c.configured, &Configuration{Addrs: c.addrs})

			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("resolveServerName(%q, %q) = %q, want an error", c.configured, c.addrs, got)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("error = %q, want it to mention %q", err, c.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("resolveServerName(%q, %q): %v", c.configured, c.addrs, err)
			}

			if got != c.want {
				t.Errorf("resolveServerName(%q, %q) = %q, want %q", c.configured, c.addrs, got, c.want)
			}
		})
	}
}

// A nil Configuration is documented as meaning the defaults, so it must infer
// the default host rather than panic or come back empty.
func TestResolveServerNameWithNilConfiguration(t *testing.T) {
	got, err := resolveServerName("", nil)
	if err != nil {
		t.Fatalf("resolveServerName with a nil config: %v", err)
	}

	if want := "cloud.mygaru.com"; got != want {
		t.Errorf("resolveServerName = %q, want %q", got, want)
	}
}

// The whole point, end to end: a client built without ServerName talks to a real
// server. Before ServerName was inferred, crypto/tls refused every handshake,
// fastrpc reconnected in the background, and the caller saw nothing but a request
// timeout - so this test fails by timing out, not by reporting a TLS error, which
// is exactly how the bug presented in production.
func TestNewWithMTLSWithoutServerName(t *testing.T) {
	certs, err := testcloud.NewCertificates()
	if err != nil {
		t.Fatalf("generate test certificates: %v", err)
	}

	server, err := testcloud.Start(testcloud.Config{TLSConfig: certs.ServerTLSConfig})
	if err != nil {
		t.Fatalf("start test-cloud: %v", err)
	}

	t.Cleanup(func() { _ = server.Close() })

	cli, err := NewWithMTLS(&Configuration{
		Addrs:                          server.Addr(),
		MaximumSimultaneousConnections: 1,
		MaxRequestDuration:             3 * time.Second,
	}, MTLSConfig{
		CertPEM:         certs.ClientCertPEM,
		KeyPEM:          certs.ClientKeyPEM,
		ServerRootCAs:   certs.ServerRoots,
		ClientCertRoots: certs.ClientRoots,
		// ServerName deliberately unset.
	})
	if err != nil {
		t.Fatalf("NewWithMTLS: %v", err)
	}

	if got := cli.clients[0].clients[0].c.TLSConfig.ServerName; got == "" {
		t.Fatal("TLSConfig.ServerName is empty, so every handshake will be refused")
	}

	req := &base.TargetRequest{
		Uids:  []*base.UID{{Id: []byte("device-id"), Type: base.UID_DEVICE_ID}},
		Match: []*base.Match_Rule{{TrafficType: base.TrafficType_TRAFFIC_TYPE_VIDEO, SegmentIds: []uint32{1}, Payer: uuid.NewString()}},
	}

	if _, sc, err := cli.Target(req); err != nil || sc != base.RPCServerResponseCode_OK {
		t.Fatalf("Target without a configured ServerName = %s, %v; want OK", sc, err)
	}
}
