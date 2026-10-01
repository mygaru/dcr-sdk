package client

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gitlab.adtelligent.com/awesome/mtls"
)

// The embedded chain, pinned: replacing a CA has to be deliberate.
var myGaruCAFingerprints = map[string]string{
	"myGaru Signinig A2": "af63741c6ff7a47dd399005964993b8767dcd9a95257e92c21a9d3f1f8cef4ad",
	"myGaru Root R2":     "1e689eca6c0e100f7b15f61c8f3401d9d81c2b02b8671fddcf00ce71d1d2e5ce",
}

func embeddedCAs(t *testing.T) []*x509.Certificate {
	t.Helper()

	var certs []*x509.Certificate
	rest := MyGaruCAChain
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return certs
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatalf("parse embedded CA: %v", err)
		}
		certs = append(certs, cert)
	}
}

func TestEmbeddedMyGaruCAChain(t *testing.T) {
	certs := embeddedCAs(t)
	if len(certs) != len(myGaruCAFingerprints) {
		t.Fatalf("embedded chain holds %d certificates, want %d", len(certs), len(myGaruCAFingerprints))
	}

	var intermediate *x509.Certificate
	for _, cert := range certs {
		sum := sha256.Sum256(cert.Raw)
		want, ok := myGaruCAFingerprints[cert.Subject.CommonName]
		if !ok || hex.EncodeToString(sum[:]) != want {
			t.Fatalf("unexpected CA %q (sha256 %x)", cert.Subject.CommonName, sum)
		}
		if !cert.IsCA {
			t.Fatalf("%q is not a CA", cert.Subject.CommonName)
		}
		if cert.Subject.CommonName == "myGaru Signinig A2" {
			intermediate = cert
		}
	}

	// the intermediate chains to the embedded root, which is how a partner
	// certificate issued by it validates without any CA pool of its own
	if _, err := intermediate.Verify(x509.VerifyOptions{
		Roots:     myGaruCAs.roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		t.Fatalf("myGaru Signinig A2 does not chain to the embedded root: %v", err)
	}
}

// Without CA pools of its own a certificate is checked against the myGaru CA,
// not the system roots: one from any other CA is refused before dialing.
func TestNewWithMTLSChecksAgainstTheMyGaruCAByDefault(t *testing.T) {
	ca, err := mtls.GenerateCA(mtls.GenerateCAConfig{CN: "not-mygaru-ca"})
	if err != nil {
		t.Fatalf("generate CA: %v", err)
	}
	cert, err := mtls.Generate(mtls.GenerateConfig{CN: "partner", UUID: uuid.NewString(), CA: ca})
	if err != nil {
		t.Fatalf("generate certificate: %v", err)
	}

	_, err = NewWithMTLS(nil, MTLSConfig{CertPEM: cert.CertPEM, KeyPEM: cert.KeyPEM})
	if err == nil || !strings.Contains(err.Error(), "validate mTLS client certificate") {
		t.Fatalf("NewWithMTLS = %v; want the certificate refused", err)
	}
}
