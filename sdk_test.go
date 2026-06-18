package dcr_sdk

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/internal/testcloud"
	"github.com/mygaru/dcr-sdk/pkg/serverauth"
	"gitlab.adtelligent.com/awesome/mtls"

	"github.com/mygaru/dcr-sdk/pkg/client"
)

const MaximumSimultaneousConnections = 4

func getTestClient(t *testing.T) *client.ShardedClient {
	t.Helper()

	server := startTestCloud(t, testcloud.Config{})
	return newTestClient(server.Addr(), MaximumSimultaneousConnections)
}

func newTestClient(addr string, maximumSimultaneousConnections int) *client.ShardedClient {
	return New(&client.Configuration{
		Addrs:                          addr,
		JwtToken:                       []byte(uuid.NewString()),
		MaximumSimultaneousConnections: maximumSimultaneousConnections,
	})
}

func TestTargetReturnsTrackingIDAndReportRoutesByIt(t *testing.T) {
	server := startTestCloud(t, testcloud.Config{ServerID: 1024})
	rpc := newTestClient(server.Addr(), 2)

	resp, sc, err := rpc.Target(&base.TargetRequest{
		Uids: []*base.UID{
			{Id: []byte(uuid.New().String()), Type: base.UID_DEVICE_ID},
		},
		Match: []*base.Match_Rule{
			{TrafficType: base.TrafficType_TRAFFIC_TYPE_VIDEO, SegmentIds: []uint32{1, 2, 3}},
		},
	})
	if nil != err {
		t.Fatalf("expected error to be %v, got %v", nil, err)
	}
	if sc != base.RPCServerResponseCode_OK {
		t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_OK, sc)
	}
	if len(resp.TrackingId) != 16 {
		t.Fatalf("expected 16-byte tracking id, got %q", resp.TrackingId)
	}
	if !rpc.IsValidTrackingID(resp.TrackingId) {
		t.Fatalf("expected tracking id %q to be valid for this client", resp.TrackingId)
	}

	report := &base.ReportRequest{
		TrackingId: resp.TrackingId,
		Event:      base.EventType_EVENT_TYPE_CLICK,
		Rules: []*base.ReportRequest_Rule{
			{
				TrafficType: base.TrafficType_TRAFFIC_TYPE_VIDEO,
				EventsCount: 1,
				SegmentIds:  []uint32{1, 2, 3},
			},
		},
	}
	sc, err = rpc.Report(report)
	if nil != err {
		t.Fatalf("expected nil, got err %v", err)
	}
	if sc != base.RPCServerResponseCode_OK {
		t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_OK, sc)
	}

	got := nextReport(t, server)
	if string(got.TrackingId) != string(report.TrackingId) {
		t.Fatalf("expected report tracking id %q, got %q", report.TrackingId, got.TrackingId)
	}
	if got.Event != report.Event {
		t.Fatalf("expected report event %s, got %s", report.Event, got.Event)
	}
}

func TestAuth(t *testing.T) {
	rpc := getTestClient(t)
	for i := 0; i < 100; i++ {
		_, sc, err := rpc.Target(testTargetRequest())
		if nil != err {
			t.Fatalf("expected nil, got err %v", err)
		}

		if sc != base.RPCServerResponseCode_OK {
			t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_OK, sc)
		}
	}

	if rpc.Reconnects() != MaximumSimultaneousConnections {
		t.Fatalf("expected %d reconnects, got %d", MaximumSimultaneousConnections, rpc.Reconnects())
	}
}

func TestAuthFailureReturnsUnauthorized(t *testing.T) {
	server := startTestCloud(t, testcloud.Config{
		AuthStatusCode: base.RPCServerResponseCode_UNAUTHORIZED,
	})
	rpc := newTestClient(server.Addr(), 1)

	_, sc, err := rpc.Target(testTargetRequest())
	if !errors.Is(err, client.ErrorUnauthorized) {
		t.Fatalf("expected unauthorized error, got %v", err)
	}
	if sc != base.RPCServerResponseCode_UNAUTHORIZED {
		t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_UNAUTHORIZED, sc)
	}
}

func TestNewWithMTLSUsesClientCertificate(t *testing.T) {
	ca, err := mtls.GenerateCA(mtls.GenerateCAConfig{CN: "dcr-sdk-test-client-ca"})
	if err != nil {
		t.Fatalf("generate client CA: %v", err)
	}
	clientCert, err := mtls.Generate(mtls.GenerateConfig{
		CN:   "dcr-sdk-client",
		UUID: uuid.NewString(),
		CA:   ca,
	})
	if err != nil {
		t.Fatalf("generate client certificate: %v", err)
	}

	serverCA, err := mtls.GenerateCA(mtls.GenerateCAConfig{CN: "dcr-sdk-test-server-ca"})
	if err != nil {
		t.Fatalf("generate server CA: %v", err)
	}
	serverTLSCert, err := newTestServerTLSCertificate(serverCA)
	if err != nil {
		t.Fatalf("generate server TLS certificate: %v", err)
	}

	clientRoots := x509.NewCertPool()
	clientRoots.AddCert(ca.Cert)
	serverRoots := x509.NewCertPool()
	serverRoots.AddCert(serverCA.Cert)

	server := startTestCloud(t, testcloud.Config{
		TLSConfig: serverauth.NewTLSConfig(&tls.Config{
			Certificates: []tls.Certificate{serverTLSCert},
			MinVersion:   tls.VersionTLS12,
		}, serverauth.MTLSConfig{Roots: clientRoots}),
	})

	rpc, err := NewWithMTLS(&client.Configuration{
		Addrs:                          server.Addr(),
		MaximumSimultaneousConnections: 1,
	}, MTLSConfig{
		CertPEM:         clientCert.CertPEM,
		KeyPEM:          clientCert.KeyPEM,
		ServerRootCAs:   serverRoots,
		ServerName:      "127.0.0.1",
		ClientCertRoots: clientRoots,
	})
	if err != nil {
		t.Fatalf("create mTLS client: %v", err)
	}

	_, sc, err := rpc.Target(testTargetRequest())
	if err != nil {
		t.Fatalf("expected nil, got err %v", err)
	}
	if sc != base.RPCServerResponseCode_OK {
		t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_OK, sc)
	}
}

func TestNewWithMTLSRejectsInvalidClientCertificate(t *testing.T) {
	cert, err := mtls.GenerateSelfSigned(mtls.GenerateConfig{
		CN:   "dcr-sdk-client",
		UUID: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("generate client certificate: %v", err)
	}

	wrongRoots := x509.NewCertPool()
	ca, err := mtls.GenerateCA(mtls.GenerateCAConfig{CN: "untrusted-ca"})
	if err != nil {
		t.Fatalf("generate CA: %v", err)
	}
	wrongRoots.AddCert(ca.Cert)

	_, err = NewWithMTLS(&client.Configuration{}, MTLSConfig{
		CertPEM:         cert.CertPEM,
		KeyPEM:          cert.KeyPEM,
		ClientCertRoots: wrongRoots,
	})
	if err == nil {
		t.Fatalf("expected invalid client certificate error")
	}
}

func TestTargetReturnsServerStatusError(t *testing.T) {
	server := startTestCloud(t, testcloud.Config{
		TargetStatus: base.RPCServerResponseCode_SERVICE_UNAVAILABLE,
	})
	rpc := newTestClient(server.Addr(), 1)

	resp, sc, err := rpc.Target(&base.TargetRequest{
		Uids: []*base.UID{
			{Id: []byte(uuid.New().String()), Type: base.UID_DEVICE_ID},
		},
	})
	if nil == err {
		t.Fatalf("expected target error")
	}
	if resp != nil {
		t.Fatalf("expected nil target response, got %+v", resp)
	}
	if sc != base.RPCServerResponseCode_SERVICE_UNAVAILABLE {
		t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_SERVICE_UNAVAILABLE, sc)
	}
}

func TestReportRejectsMissingAndUnknownTrackingID(t *testing.T) {
	rpc := getTestClient(t)

	sc, err := rpc.Report(&base.ReportRequest{})
	if nil == err {
		t.Fatalf("expected missing tracking id error")
	}
	if sc != base.RPCServerResponseCode_UNKNOWN {
		t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_UNKNOWN, sc)
	}

	sc, err = rpc.Report(&base.ReportRequest{TrackingId: []byte("FFFF000000000001")})
	if nil == err {
		t.Fatalf("expected unknown server error")
	}
	if sc != base.RPCServerResponseCode_UNKNOWN {
		t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_UNKNOWN, sc)
	}
}

func TestNewPanicsOnNilConfiguration(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic")
		}
	}()
	New(nil)
}

func TestNewWithTLSPanicsOnNilTLSConfig(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic")
		}
	}()
	NewWithTLS(&client.Configuration{}, nil)
}

func TestNew(t *testing.T) {

	tests := []struct {
		name       string
		cfg        *client.Configuration
		expectAddr string
	}{
		{
			name:       "nil configuration",
			cfg:        &client.Configuration{},
			expectAddr: "cloud.mygaru.com:7937",
		},
		{
			name: "addr configuration",
			cfg: &client.Configuration{
				Addrs: "anyaddr.here:8080",
			},
			expectAddr: "anyaddr.here:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sdk := New(tt.cfg)
			if sdk.Configuration.Addrs != tt.expectAddr {
				t.Errorf("expected addrs to be %q, got %q", tt.expectAddr, sdk.Configuration.Addrs)
			}
		})
	}
}

func testTargetRequest() *base.TargetRequest {
	return &base.TargetRequest{
		Uids: []*base.UID{
			{Id: []byte(uuid.New().String()), Type: base.UID_DEVICE_ID},
		},
		Match: []*base.Match_Rule{
			{TrafficType: base.TrafficType_TRAFFIC_TYPE_VIDEO, SegmentIds: []uint32{1, 2, 3}},
		},
	}
}

func startTestCloud(t *testing.T, cfg testcloud.Config) *testcloud.Server {
	t.Helper()

	server, err := testcloud.Start(cfg)
	if err != nil {
		t.Fatalf("start test-cloud: %v", err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("stop test-cloud: %v", err)
		}
	})
	return server
}

func nextReport(t *testing.T, server *testcloud.Server) *base.ReportRequest {
	t.Helper()

	select {
	case report := <-server.Reports():
		return report
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for report request")
		return nil
	}
}

func newTestServerTLSCertificate(ca *mtls.Certificate) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate server key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate server serial: %w", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "127.0.0.1",
		},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, &key.PublicKey, ca.Key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create server certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse server certificate: %w", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
		Leaf:        leaf,
	}, nil
}
