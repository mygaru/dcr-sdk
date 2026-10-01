package client

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/VictoriaMetrics/metrics"
)

const (
	defaultRenewBefore        = 7 * 24 * time.Hour
	defaultRenewCheckInterval = 12 * time.Hour
	// renewRetryInterval caps how long a failed renewal waits to be tried again.
	renewRetryInterval = time.Hour
	// renewFetchTimeout bounds one renewal at the CA, polling included.
	renewFetchTimeout = 2 * time.Minute
)

var (
	certRenewed = metrics.GetOrCreateCounter(`dcrRPCClientCertRenew{result="renewed"}`)
	certSame    = metrics.GetOrCreateCounter(`dcrRPCClientCertRenew{result="same"}`)
	certFailed  = metrics.GetOrCreateCounter(`dcrRPCClientCertRenew{result="failed"}`)
)

// autoRenewer renews a client's certificate before it expires; see
// MTLSConfig.OnRenewed.
type autoRenewer struct {
	sc *ShardedClient

	before, every time.Duration
	caURL         string
	onRenewed     func(certPEM, keyPEM []byte) error
	onError       func(error)
	now           func() time.Time
}

// newAutoRenewer is the renewer cfg asks for, or nil when it asks for none.
func newAutoRenewer(sc *ShardedClient, cfg MTLSConfig) *autoRenewer {
	if cfg.OnRenewed == nil || cfg.RenewBefore < 0 {
		return nil
	}

	r := &autoRenewer{
		sc:        sc,
		before:    cfg.RenewBefore,
		every:     cfg.RenewCheckInterval,
		caURL:     cfg.CAURL,
		onRenewed: cfg.OnRenewed,
		onError:   cfg.OnRenewError,
		now:       time.Now,
	}
	if r.before == 0 {
		r.before = defaultRenewBefore
	}
	if r.every <= 0 {
		r.every = defaultRenewCheckInterval
	}
	if r.onError == nil {
		r.onError = func(err error) { log.Printf("dcr-sdk: client certificate renewal failed: %v", err) }
	}
	return r
}

// run checks the certificate now and every interval after, for the life of
// the process.
func (r *autoRenewer) run() {
	for {
		wait := r.every
		if _, err := r.check(context.Background()); err != nil {
			certFailed.Inc()
			r.onError(err)
			wait = min(wait, renewRetryInterval)
		}
		time.Sleep(wait)
	}
}

// check renews the certificate if it expires within before, and reports
// whether it did.
func (r *autoRenewer) check(ctx context.Context) (bool, error) {
	provider := r.sc.certProvider
	leaf := provider.Leaf()
	if leaf.NotAfter.Sub(r.now()) > r.before {
		return false, nil
	}

	fetchCtx, cancel := context.WithTimeout(ctx, renewFetchTimeout)
	defer cancel()

	certPEM, keyPEM, err := provider.Fetch(fetchCtx, r.caURL)
	if err != nil {
		return false, err
	}

	renewed, err := leafOf(certPEM)
	if err != nil {
		return false, err
	}
	if renewed.SerialNumber.Cmp(leaf.SerialNumber) == 0 {
		// the CA has nothing newer yet; asked again next interval
		certSame.Inc()
		return false, nil
	}

	// Validated before it is handed to the application, so what is saved is
	// what the client will use.
	if _, err := provider.check.load(certPEM, keyPEM); err != nil {
		return false, fmt.Errorf("renewed certificate %s: %w", renewed.SerialNumber, err)
	}
	if err := r.onRenewed(certPEM, keyPEM); err != nil {
		return false, fmt.Errorf("save renewed certificate %s: %w", renewed.SerialNumber, err)
	}
	if err := provider.Update(certPEM, keyPEM); err != nil {
		return false, fmt.Errorf("use renewed certificate %s: %w", renewed.SerialNumber, err)
	}

	r.sc.Reconnect(false)
	certRenewed.Inc()
	return true, nil
}

func leafOf(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, errors.New("renewed certificate holds no PEM block")
	}
	return x509.ParseCertificate(block.Bytes)
}
