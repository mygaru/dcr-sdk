package client

import (
	"testing"

	"github.com/google/uuid"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/internal/testcloud"
)

// TestPlaintextConnectionIsRefused pins what makes NewWithMTLS the only
// constructor worth having: a connection without a client certificate - which
// only the unexported newClient can still open - gets nothing from a cloud that
// serves mTLS only.
func TestPlaintextConnectionIsRefused(t *testing.T) {
	certs, err := testcloud.NewCertificates()
	if err != nil {
		t.Fatalf("generate test certificates: %v", err)
	}
	server, err := testcloud.Start(testcloud.Config{TLSConfig: certs.ServerTLSConfig})
	if err != nil {
		t.Fatalf("start test-cloud: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	req := func() *base.TargetRequest {
		return &base.TargetRequest{
			Uids:  []*base.UID{{Id: []byte("device-id"), Type: base.UID_DEVICE_ID}},
			Match: []*base.Match_Rule{{TrafficType: base.TrafficType_TRAFFIC_TYPE_VIDEO, SegmentIds: []uint32{1}, Payer: uuid.NewString()}},
		}
	}

	plaintext := newClient(&Configuration{Addrs: server.Addr(), MaximumSimultaneousConnections: 1}, nil)
	if _, sc, err := plaintext.Target(req()); err == nil || sc != base.RPCServerResponseCode_UNAUTHORIZED {
		t.Fatalf("plaintext Target = %s, %v; want UNAUTHORIZED", sc, err)
	}

	mtlsClient, err := NewWithMTLS(&Configuration{Addrs: server.Addr(), MaximumSimultaneousConnections: 1}, MTLSConfig{
		CertPEM:         certs.ClientCertPEM,
		KeyPEM:          certs.ClientKeyPEM,
		ServerRootCAs:   certs.ServerRoots,
		ServerName:      certs.ServerName,
		ClientCertRoots: certs.ClientRoots,
	})
	if err != nil {
		t.Fatalf("NewWithMTLS: %v", err)
	}
	if _, sc, err := mtlsClient.Target(req()); err != nil || sc != base.RPCServerResponseCode_OK {
		t.Fatalf("mTLS Target = %s, %v; want OK", sc, err)
	}
}
