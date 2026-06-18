package serverauth

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"gitlab.adtelligent.com/awesome/mtls"
)

// Conn is the connection state shared by legacy RPC auth and mTLS auth.
type Conn interface {
	net.Conn
	GetUUID() uuid.UUID
	SetUUID(uuid.UUID)
	RequestsCount() uint64
	IncrementRequests() uint64
}

type authConn struct {
	net.Conn

	requests atomic.Uint64

	mu     sync.RWMutex
	authID uuid.UUID
}

// GetUUID returns the authenticated payer/client UUID for this connection.
func (c *authConn) GetUUID() uuid.UUID {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.authID
}

// SetUUID stores the authenticated payer/client UUID for this connection.
func (c *authConn) SetUUID(uid uuid.UUID) {
	c.mu.Lock()
	c.authID = uid
	c.mu.Unlock()
}

// RequestsCount returns the number of requests observed on this connection.
func (c *authConn) RequestsCount() uint64 {
	return c.requests.Load()
}

// IncrementRequests increments and returns the number of requests observed on this connection.
func (c *authConn) IncrementRequests() uint64 {
	return c.requests.Add(1)
}

// Listener wraps accepted connections with auth state.
type Listener struct {
	net.Listener
}

// NewListener wraps accepted connections with auth state.
func NewListener(ln net.Listener) net.Listener {
	return &Listener{Listener: ln}
}

// Accept accepts a connection and attaches auth state to it.
func (ln *Listener) Accept() (net.Conn, error) {
	c, err := ln.Listener.Accept()
	if err != nil {
		if c != nil {
			panic(fmt.Sprintf("BUG: accept returned non-nil c=%#v with error %s", c, err))
		}
		return nil, err
	}
	return &authConn{Conn: c}, nil
}

// GetConn returns the auth connection state attached by Listener.
func GetConn(conn net.Conn) (Conn, bool) {
	authConn, ok := conn.(Conn)
	return authConn, ok
}

// GetUUID returns a non-zero authenticated UUID from conn.
func GetUUID(conn net.Conn) (uuid.UUID, bool) {
	authConn, ok := GetConn(conn)
	if !ok {
		return uuid.Nil, false
	}
	uid := authConn.GetUUID()
	return uid, uid != uuid.Nil
}

// SetUUID stores uid on conn or returns an error if conn was not created by Listener.
func SetUUID(conn net.Conn, uid uuid.UUID) error {
	authConn, ok := GetConn(conn)
	if !ok {
		return fmt.Errorf("connection auth is unavailable")
	}
	authConn.SetUUID(uid)
	return nil
}

// MTLSConfig describes how client certificates are authenticated.
type MTLSConfig struct {
	// Roots is the CA pool used to verify client certificates.
	Roots *x509.CertPool
	// Intermediates is the optional CA pool used to build client certificate chains.
	Intermediates *x509.CertPool
	// CurrentTime optionally overrides certificate validity checks. When zero, current time is used.
	CurrentTime time.Time
	// Issuer is the certificate issuer used for OCSP status checks.
	Issuer *x509.Certificate
	// StatusClient optionally overrides the HTTP client used for OCSP checks.
	StatusClient *http.Client
	// RequireOCSP controls whether the certificate must have a good OCSP status.
	RequireOCSP bool
}

// NewTLSConfig returns a fastrpc-compatible TLS config that stores mTLS identity
// on connections created by NewListener.
//
// fastrpc performs its own plaintext/TLS negotiation before the TLS handshake,
// so a single server with TLSConfig set can accept both old plaintext clients
// and new mTLS clients on the same listener.
func NewTLSConfig(base *tls.Config, auth MTLSConfig) *tls.Config {
	if base == nil {
		base = &tls.Config{}
	}
	cfg := base.Clone()
	if cfg.ClientAuth == tls.NoClientCert {
		cfg.ClientAuth = tls.RequireAnyClientCert
	}
	if cfg.MinVersion == 0 {
		cfg.MinVersion = tls.VersionTLS12
	}

	previousGetConfig := cfg.GetConfigForClient
	cfg.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		selected := cfg
		if previousGetConfig != nil {
			next, err := previousGetConfig(hello)
			if err != nil {
				return nil, err
			}
			if next != nil {
				selected = next
			}
		}

		authConn, _ := GetConn(hello.Conn)
		child := selected.Clone()
		previousVerify := child.VerifyPeerCertificate
		child.GetConfigForClient = nil
		child.VerifyPeerCertificate = func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
			if previousVerify != nil {
				if err := previousVerify(rawCerts, verifiedChains); err != nil {
					return err
				}
			}

			uid, err := authenticateClientCertificate(rawCerts, auth)
			if err != nil {
				return err
			}
			if authConn == nil {
				return fmt.Errorf("connection auth is unavailable")
			}
			authConn.SetUUID(uid)
			return nil
		}
		return child, nil
	}

	return cfg
}

func authenticateClientCertificate(rawCerts [][]byte, cfg MTLSConfig) (uuid.UUID, error) {
	if len(rawCerts) == 0 {
		return uuid.Nil, fmt.Errorf("client certificate is required")
	}

	leaf, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse client certificate: %w", err)
	}

	if err := mtls.CheckTLS(leaf, mtls.CheckTLSConfig{
		Roots:         cfg.Roots,
		Intermediates: cfg.Intermediates,
		CurrentTime:   cfg.CurrentTime,
	}); err != nil {
		return uuid.Nil, fmt.Errorf("validate client certificate: %w", err)
	}

	if cfg.RequireOCSP {
		err, status := mtls.CheckStatus(leaf, mtls.CheckStatusConfig{
			Issuer: cfg.Issuer,
			Client: cfg.StatusClient,
		})
		if err != nil {
			return uuid.Nil, fmt.Errorf("check client certificate status: %w", err)
		}
		if status != mtls.CertStatusGood {
			return uuid.Nil, fmt.Errorf("client certificate status is not good: %v", status)
		}
	}

	rawUUID := mtls.GetUUID(leaf)
	if rawUUID == "" {
		return uuid.Nil, fmt.Errorf("client certificate UUID is missing")
	}
	uid, err := uuid.Parse(rawUUID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse client certificate UUID: %w", err)
	}
	return uid, nil
}
