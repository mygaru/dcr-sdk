package serverauth

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/aradilov/fastrpc"
	"github.com/aradilov/fastrpc/tlv"
	"github.com/google/uuid"
	"gitlab.adtelligent.com/awesome/mtls"
)

func TestServerAcceptsPlaintextAuthAndMTLSOnSameListener(t *testing.T) {
	clientCA, err := mtls.GenerateCA(mtls.GenerateCAConfig{CN: "serverauth-client-ca"})
	if err != nil {
		t.Fatalf("generate client CA: %v", err)
	}
	serverCA, err := mtls.GenerateCA(mtls.GenerateCAConfig{CN: "serverauth-server-ca"})
	if err != nil {
		t.Fatalf("generate server CA: %v", err)
	}
	serverCert, err := newTestServerCertificate(serverCA)
	if err != nil {
		t.Fatalf("generate server certificate: %v", err)
	}

	clientRoots := x509.NewCertPool()
	clientRoots.AddCert(clientCA.Cert)
	serverRoots := x509.NewCertPool()
	serverRoots.AddCert(serverCA.Cert)

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ln = NewListener(ln)

	legacyUUID := uuid.New()
	server := &fastrpc.Server{
		SniffHeader:     "serverauth-test",
		ProtocolVersion: 1,
		NewHandlerCtx: func() fastrpc.HandlerCtx {
			return &tlv.RequestCtx{}
		},
		Handler: func(ctxv fastrpc.HandlerCtx) fastrpc.HandlerCtx {
			ctx := ctxv.(*tlv.RequestCtx)
			switch {
			case bytes.Equal(ctx.Request.Name(), []byte("auth")):
				if err := MustSetUUID(ctx.Conn(), legacyUUID); err != nil {
					ctx.Response.Append([]byte(err.Error()))
					return ctx
				}
				ctx.Response.Append([]byte("ok"))
			case bytes.Equal(ctx.Request.Name(), []byte("who")):
				uid, ok := UUIDFromConn(ctx.Conn())
				if !ok {
					ctx.Response.Append([]byte("unauthorized"))
					return ctx
				}
				ctx.Response.Append([]byte(uid.String()))
			default:
				ctx.Response.Append([]byte("unknown request"))
			}
			return ctx
		},
		TLSConfig: NewTLSConfig(&tls.Config{
			Certificates: []tls.Certificate{serverCert},
		}, MTLSConfig{
			Roots: clientRoots,
		}),
		CompressType:     fastrpc.CompressSnappy,
		PipelineRequests: true,
	}

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ln)
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		select {
		case err := <-done:
			if err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
				t.Fatalf("serve: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatalf("server did not stop")
		}
	})

	plaintext := newTLVClient(ln.Addr().String(), nil)
	if got := doTLV(t, plaintext, "auth"); got != "ok" {
		t.Fatalf("unexpected plaintext auth response: %q", got)
	}
	if got := doTLV(t, plaintext, "who"); got != legacyUUID.String() {
		t.Fatalf("expected legacy UUID %q, got %q", legacyUUID, got)
	}

	mtlsUUID := uuid.New()
	clientCert, err := mtls.Generate(mtls.GenerateConfig{
		CN:   "serverauth-mtls-client",
		UUID: mtlsUUID.String(),
		CA:   clientCA,
	})
	if err != nil {
		t.Fatalf("generate client certificate: %v", err)
	}
	tlsClientCert, err := tls.X509KeyPair(clientCert.CertPEM, clientCert.KeyPEM)
	if err != nil {
		t.Fatalf("parse client certificate: %v", err)
	}

	encrypted := newTLVClient(ln.Addr().String(), &tls.Config{
		Certificates: []tls.Certificate{tlsClientCert},
		RootCAs:      serverRoots,
		ServerName:   "127.0.0.1",
		MinVersion:   tls.VersionTLS12,
	})
	if got := doTLV(t, encrypted, "who"); got != mtlsUUID.String() {
		t.Fatalf("expected mTLS UUID %q, got %q", mtlsUUID, got)
	}
}

func newTLVClient(addr string, tlsConfig *tls.Config) *fastrpc.Client {
	return &fastrpc.Client{
		SniffHeader:     "serverauth-test",
		ProtocolVersion: 1,
		Addr:            addr,
		NewResponse: func() fastrpc.ResponseReader {
			return &tlv.Response{}
		},
		TLSConfig:    tlsConfig,
		CompressType: fastrpc.CompressSnappy,
	}
}

func doTLV(t *testing.T, client *fastrpc.Client, name string) string {
	t.Helper()

	var req tlv.Request
	var resp tlv.Response
	req.SetName(name)
	if err := client.DoDeadline(&req, &resp, time.Now().Add(5*time.Second)); err != nil {
		t.Fatalf("%s request: %v", name, err)
	}
	return string(resp.Value())
}

func newTestServerCertificate(ca *mtls.Certificate) (tls.Certificate, error) {
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
