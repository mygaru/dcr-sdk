package client

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/pem"
	"fmt"
	"net"
	"strings"
	"time"

	"gitlab.adtelligent.com/awesome/mtls"
)

// MTLSConfig contains certificate material and validation settings for an mTLS client.
type MTLSConfig struct {
	// CertPEM is the PEM-encoded client certificate. It may include intermediate certificates.
	CertPEM []byte
	// KeyPEM is the PEM-encoded private key for CertPEM.
	KeyPEM []byte
	// ServerRootCAs is the CA pool used to verify the RPC server certificate.
	ServerRootCAs *x509.CertPool
	// ServerName is used for RPC server certificate hostname verification.
	ServerName string
	// MinVersion optionally overrides the minimum TLS version. When zero, TLS 1.2 is used.
	MinVersion uint16
	// ClientCertRoots is the CA pool used by mtls.CheckTLS to validate CertPEM.
	// When nil, the myGaru root CA the SDK carries is used (see MyGaruCAChain).
	ClientCertRoots *x509.CertPool
	// ClientCertIntermediates is an optional intermediate CA pool used by
	// mtls.CheckTLS. When nil, the myGaru intermediate CA the SDK carries is
	// used, so a CertPEM without its chain still validates. Intermediates that
	// follow the certificate in CertPEM are added either way.
	ClientCertIntermediates *x509.CertPool
	// CurrentTime optionally overrides certificate validity checks. When zero, the current time is used.
	CurrentTime time.Time

	// OnRenewed turns on automatic renewal: once RenewBefore of the
	// certificate's validity is left, the client renews it at the CA (CAURL),
	// calls OnRenewed with the new certificate and key, then serves them to new
	// connections and moves the open ones onto them (Reconnect, graceful).
	//
	// OnRenewed is where the application saves them wherever it loads its
	// certificate from, so a restart picks up the renewed one: SaveToFiles for
	// a certificate in two PEM files, or a function of its own for a secret
	// store. If it returns an error the renewal is dropped, the current
	// certificate stays and the renewal is tried again later. Without
	// OnRenewed nothing is renewed automatically: a certificate renewed only in
	// memory would be lost on the next restart.
	OnRenewed func(certPEM, keyPEM []byte) error
	// OnRenewError is told why an automatic renewal failed; the client keeps
	// the current certificate and tries again. When nil the error is logged
	// with the standard log package.
	OnRenewError func(error)
	// RenewBefore is how long before the certificate expires it is renewed
	// automatically: 7 days when zero. Negative turns automatic renewal off.
	RenewBefore time.Duration
	// RenewCheckInterval is how often the certificate is checked: 12 hours
	// when zero. A failed renewal is retried within an hour.
	RenewCheckInterval time.Duration
	// CAURL is the CA a certificate is renewed at: DefaultCAURL when empty.
	CAURL string
}

// NewWithMTLS creates a client of the DCR cloud that authenticates with a client
// certificate. It is the only way to create one: the cloud identifies the
// partner by its certificate, so there is no plaintext or tokenless connection
// to fall back to.
//
// The certificate is obtained from the manager or downloaded from the profile
// section of the member zone. cfg may be nil for the defaults.
//
// The certificate is served to every new connection by the client's
// CertProvider, so a renewed one (CertProvider.Fetch, CertProvider.Update)
// takes effect without creating the client again; Reconnect moves the open
// connections onto it.
func NewWithMTLS(cfg *Configuration, mtlsCfg MTLSConfig) (*ShardedClient, error) {
	return newWithMTLS(cfg, mtlsCfg, nil)
}

// newWithMTLS is NewWithMTLS with a hook that sets the provider up before
// automatic renewal starts with it; for tests.
func newWithMTLS(cfg *Configuration, mtlsCfg MTLSConfig, setup func(*CertProvider)) (*ShardedClient, error) {
	provider, err := newCertProvider(mtlsCfg)
	if err != nil {
		return nil, err
	}
	if setup != nil {
		setup(provider)
	}

	minVersion := mtlsCfg.MinVersion
	if minVersion == 0 {
		minVersion = tls.VersionTLS12
	}

	serverName, err := resolveServerName(mtlsCfg.ServerName, cfg)
	if err != nil {
		return nil, err
	}

	sc := newClient(cfg, &tls.Config{
		// Asked on every handshake, so a new connection always presents the
		// current certificate - and presents it whichever CAs the server
		// names, which leaves refusing it to the server and its error.
		GetClientCertificate: provider.GetClientCertificate,
		// nil: the system roots, which the cloud's (ACME) certificate is
		// issued under
		RootCAs:    mtlsCfg.ServerRootCAs,
		ServerName: serverName,
		MinVersion: minVersion,
	})
	sc.certProvider = provider

	if renewer := newAutoRenewer(sc, mtlsCfg); renewer != nil {
		go renewer.run()
	}
	return sc, nil
}

// certCheck is what a client certificate is validated against.
type certCheck struct {
	roots, intermediates *x509.CertPool
	currentTime          time.Time
}

func newCertCheck(cfg MTLSConfig) certCheck {
	check := certCheck{
		roots:         cfg.ClientCertRoots,
		intermediates: cfg.ClientCertIntermediates,
		currentTime:   cfg.CurrentTime,
	}
	if check.roots == nil {
		check.roots = myGaruCAs.roots
	}
	if check.intermediates == nil {
		check.intermediates = myGaruCAs.intermediates
	}
	return check
}

// load parses a client certificate and its key and validates the certificate
// with mtls.CheckTLS, so a certificate the cloud would refuse is reported when
// it is handed to the SDK rather than on every dial.
func (check certCheck) load(certPEM, keyPEM []byte) (tls.Certificate, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse mTLS client certificate: %w", err)
	}
	if len(cert.Certificate) == 0 {
		return tls.Certificate{}, fmt.Errorf("mTLS client certificate is empty")
	}

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse mTLS leaf certificate: %w", err)
	}
	cert.Leaf = leaf

	intermediates := check.intermediates
	if len(cert.Certificate) > 1 {
		// never added to a pool the caller or the defaults own
		intermediates = intermediates.Clone()
		for _, certDER := range cert.Certificate[1:] {
			intermediate, err := x509.ParseCertificate(certDER)
			if err != nil {
				return tls.Certificate{}, fmt.Errorf("parse mTLS intermediate certificate: %w", err)
			}
			intermediates.AddCert(intermediate)
		}
	}

	if err := mtls.CheckTLS(leaf, mtls.CheckTLSConfig{
		Roots:         check.roots,
		Intermediates: intermediates,
		CurrentTime:   check.currentTime,
	}); err != nil {
		return tls.Certificate{}, fmt.Errorf("validate mTLS client certificate: %w", err)
	}

	return cert, nil
}

// MyGaruCAChain is the PEM chain of the CA that issues DCR cloud client
// certificates (http://ca.mygaru.com/ca-chain): the intermediate "myGaru
// Signinig A2" and the root "myGaru Root R2". A client certificate is checked
// against it unless MTLSConfig names CA pools of its own. It is public: a CA
// certificate carries the CA's public key, never the key that signs.
//
//go:embed mygaru-ca-chain.pem
var MyGaruCAChain []byte

// myGaruCAs is MyGaruCAChain split into its root and its intermediates.
var myGaruCAs = func() (cas struct{ roots, intermediates *x509.CertPool }) {
	cas.roots = x509.NewCertPool()
	cas.intermediates = x509.NewCertPool()

	rest := MyGaruCAChain
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			panic(fmt.Sprintf("dcr-sdk: embedded myGaru CA chain: %v", err))
		}

		if bytes.Equal(cert.RawSubject, cert.RawIssuer) && cert.CheckSignatureFrom(cert) == nil {
			cas.roots.AddCert(cert)
		} else {
			cas.intermediates.AddCert(cert)
		}
	}
	return cas
}()

// resolveServerName settles what the server certificate is verified against.
//
// crypto/tls refuses a handshake when neither ServerName nor InsecureSkipVerify
// is set, and nothing fills it in for us: Go infers the name from the address in
// tls.Dial, but the connection here is dialled as plain TCP and wrapped
// afterwards, so an unset ServerName fails every handshake. Worse, it fails
// invisibly - fastrpc reconnects in the background and the caller sees only its
// request time out, with no mention of TLS - so a caller who simply did not know
// about the field gets a client that times out 100% of the time and no clue why.
//
// So infer it from the address, which is what the caller would have typed anyway,
// and refuse to build a client when that cannot be done rather than handing back
// one that cannot connect. Shards on different hosts have no single name to
// verify against, which is the one case the caller has to settle.
func resolveServerName(configured string, cfg *Configuration) (string, error) {
	if configured != "" {
		return configured, nil
	}

	addrs := normalizeConfiguration(cfg).Addrs

	var name string

	for _, addr := range strings.Split(addrs, ",") {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}

		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}

		switch {
		case host == "":
			continue
		case name == "":
			name = host
		case host != name:
			return "", fmt.Errorf("MTLSConfig.ServerName is empty and cannot be inferred: "+
				"Addrs names more than one host (%q and %q), so set it to the name the "+
				"server certificate is issued for", name, host)
		}
	}

	if name == "" {
		return "", fmt.Errorf("MTLSConfig.ServerName is empty and cannot be inferred from Addrs %q: "+
			"set it to the name the server certificate is issued for", addrs)
	}

	return name, nil
}
