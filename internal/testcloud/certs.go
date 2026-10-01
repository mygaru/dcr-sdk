package testcloud

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"time"

	"github.com/google/uuid"
	"gitlab.adtelligent.com/awesome/mtls"
	"github.com/mygaru/dcr-sdk/pkg/serverauth"
)

// Certificates is a throwaway PKI for tests: a client CA with one client
// certificate, and a server CA with a certificate for 127.0.0.1.
type Certificates struct {
	// ClientID is the partner UUID in the client certificate.
	ClientID uuid.UUID
	// ClientCertPEM and ClientKeyPEM are the client's certificate and key.
	ClientCertPEM []byte
	ClientKeyPEM  []byte
	// ClientRoots verifies ClientCertPEM.
	ClientRoots *x509.CertPool
	// ServerRoots verifies the server certificate of ServerTLSConfig.
	ServerRoots *x509.CertPool
	// ServerName is the name the server certificate is issued for.
	ServerName string
	// ServerTLSConfig is a Config.TLSConfig that accepts the client certificate.
	ServerTLSConfig *tls.Config

	clientCA *mtls.Certificate
}

// NewClientCertificate issues another client certificate from the client CA,
// for partner id: a renewal, as far as a test can tell.
func (c *Certificates) NewClientCertificate(id uuid.UUID) (certPEM, keyPEM []byte, err error) {
	cert, err := mtls.Generate(mtls.GenerateConfig{CN: "dcr-sdk-client", UUID: id.String(), CA: c.clientCA})
	if err != nil {
		return nil, nil, fmt.Errorf("generate client certificate: %w", err)
	}
	return cert.CertPEM, cert.KeyPEM, nil
}

// NewCertificates generates a fresh test PKI.
func NewCertificates() (*Certificates, error) {
	clientCA, err := mtls.GenerateCA(mtls.GenerateCAConfig{CN: "dcr-sdk-test-client-ca"})
	if err != nil {
		return nil, fmt.Errorf("generate client CA: %w", err)
	}

	clientID := uuid.New()
	clientCert, err := mtls.Generate(mtls.GenerateConfig{
		CN:   "dcr-sdk-client",
		UUID: clientID.String(),
		CA:   clientCA,
	})
	if err != nil {
		return nil, fmt.Errorf("generate client certificate: %w", err)
	}

	serverCA, err := mtls.GenerateCA(mtls.GenerateCAConfig{CN: "dcr-sdk-test-server-ca"})
	if err != nil {
		return nil, fmt.Errorf("generate server CA: %w", err)
	}
	serverCert, err := newServerCertificate(serverCA)
	if err != nil {
		return nil, err
	}

	clientRoots := x509.NewCertPool()
	clientRoots.AddCert(clientCA.Cert)
	serverRoots := x509.NewCertPool()
	serverRoots.AddCert(serverCA.Cert)

	return &Certificates{
		ClientID:      clientID,
		ClientCertPEM: clientCert.CertPEM,
		ClientKeyPEM:  clientCert.KeyPEM,
		ClientRoots:   clientRoots,
		ServerRoots:   serverRoots,
		ServerName:    "127.0.0.1",
		ServerTLSConfig: serverauth.NewTLSConfig(&tls.Config{
			Certificates: []tls.Certificate{serverCert},
			MinVersion:   tls.VersionTLS12,
		}, serverauth.MTLSConfig{Roots: clientRoots}),
		clientCA: clientCA,
	}, nil
}

func newServerCertificate(ca *mtls.Certificate) (tls.Certificate, error) {
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
