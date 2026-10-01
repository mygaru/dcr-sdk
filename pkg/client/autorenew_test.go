package client

import (
	"context"
	"crypto/x509"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/internal/testcloud"
)

// renewFixture is a client of a test cloud whose certificate the fake CA
// renews, with a renewer whose clock the test sets.
type renewFixture struct {
	certs   *testcloud.Certificates
	cloud   *testcloud.Server
	ca      *fakeCA
	sc      *ShardedClient
	renewer *autoRenewer

	renewedID uuid.UUID
	saved     [][2][]byte
	saveErr   error
}

func newRenewFixture(t *testing.T) *renewFixture {
	t.Helper()

	f := &renewFixture{certs: newTestCertificates(t), ca: &fakeCA{pending: 1}, renewedID: uuid.New()}
	f.ca.issued[0], f.ca.issued[1] = issue(t, f.certs, f.renewedID)
	caServer := startFakeCA(t, f.ca)

	cloud, err := testcloud.Start(testcloud.Config{TLSConfig: f.certs.ServerTLSConfig})
	if err != nil {
		t.Fatalf("start test-cloud: %v", err)
	}
	t.Cleanup(func() { _ = cloud.Close() })
	f.cloud = cloud

	cfg := testMTLSConfigFor(f.certs, f.certs.ClientCertPEM, f.certs.ClientKeyPEM)
	cfg.CAURL = caServer.URL
	cfg.OnRenewed = func(certPEM, keyPEM []byte) error {
		f.saved = append(f.saved, [2][]byte{certPEM, keyPEM})
		return f.saveErr
	}
	// renewal is driven by the test, through f.renewer
	cfg.RenewBefore = -1

	f.sc, err = newWithMTLS(&Configuration{Addrs: cloud.Addr(), MaximumSimultaneousConnections: 1}, cfg, func(p *CertProvider) {
		p.caRoots = x509.NewCertPool()
		p.caRoots.AddCert(caServer.Certificate())
		p.pollInterval = 10 * time.Millisecond
	})
	if err != nil {
		t.Fatalf("NewWithMTLS: %v", err)
	}

	cfg.RenewBefore = 0 // the default, 7 days
	f.renewer = newAutoRenewer(f.sc, cfg)
	return f
}

// at sets the renewer's clock to before the certificate expires.
func (f *renewFixture) at(beforeExpiry time.Duration) {
	notAfter := f.sc.CertProvider().Leaf().NotAfter
	f.renewer.now = func() time.Time { return notAfter.Add(-beforeExpiry) }
}

func (f *renewFixture) target(t *testing.T) uuid.UUID {
	t.Helper()
	if _, sc, err := f.sc.Target(&base.TargetRequest{
		Uids: []*base.UID{{Id: []byte("device-id"), Type: base.UID_DEVICE_ID}},
	}); err != nil {
		t.Fatalf("Target = %s, %v", sc, err)
	}
	return f.cloud.LastCaller()
}

func TestAutoRenewLeavesACertificateWithMoreThanAWeekLeft(t *testing.T) {
	f := newRenewFixture(t)
	f.at(8 * 24 * time.Hour)

	renewed, err := f.renewer.check(context.Background())
	if err != nil || renewed || len(f.ca.asked) != 0 {
		t.Fatalf("check = %t, %v after %d CA calls; want nothing done", renewed, err, len(f.ca.asked))
	}
}

func TestAutoRenewAWeekBeforeExpiry(t *testing.T) {
	f := newRenewFixture(t)
	if got := f.target(t); got != f.certs.ClientID {
		t.Fatalf("first Target came as %s", got)
	}

	f.at(6 * 24 * time.Hour)
	renewed, err := f.renewer.check(context.Background())
	if err != nil || !renewed {
		t.Fatalf("check = %t, %v; want renewed", renewed, err)
	}

	if len(f.saved) != 1 || string(f.saved[0][0]) != string(f.ca.issued[0]) || string(f.saved[0][1]) != string(f.ca.issued[1]) {
		t.Fatal("OnRenewed was not handed what the CA issued")
	}
	if string(f.sc.CertProvider().CertPEM()) != string(f.ca.issued[0]) {
		t.Fatal("the client does not use the renewed certificate")
	}
	// the open connection was moved onto it
	if got := f.target(t); got != f.renewedID {
		t.Fatalf("Target after renewal came as %s, want the renewed %s", got, f.renewedID)
	}
}

func TestAutoRenewKeepsTheCertificateWhenItCannotBeSaved(t *testing.T) {
	f := newRenewFixture(t)
	f.saveErr = errors.New("disk full")
	held := f.sc.CertProvider().CertPEM()

	f.at(24 * time.Hour)
	if renewed, err := f.renewer.check(context.Background()); err == nil || renewed {
		t.Fatalf("check = %t, %v; want the save error", renewed, err)
	}
	if string(f.sc.CertProvider().CertPEM()) != string(held) {
		t.Fatal("a renewal that was not saved replaced the certificate")
	}
}

func TestAutoRenewIgnoresTheSameCertificateBack(t *testing.T) {
	f := newRenewFixture(t)
	f.ca.issued[0], f.ca.issued[1] = f.certs.ClientCertPEM, f.certs.ClientKeyPEM

	f.at(24 * time.Hour)
	if renewed, err := f.renewer.check(context.Background()); err != nil || renewed || len(f.saved) != 0 {
		t.Fatalf("check = %t, %v, %d saves; want the same certificate left alone", renewed, err, len(f.saved))
	}
}

func TestAutoRenewIsOnlyOnWithOnRenewed(t *testing.T) {
	sc := &ShardedClient{}
	save := func([]byte, []byte) error { return nil }

	if newAutoRenewer(sc, MTLSConfig{}) != nil {
		t.Fatal("renewing without OnRenewed would lose the certificate on restart")
	}
	if newAutoRenewer(sc, MTLSConfig{OnRenewed: save, RenewBefore: -1}) != nil {
		t.Fatal("a negative RenewBefore must turn renewal off")
	}

	r := newAutoRenewer(sc, MTLSConfig{OnRenewed: save})
	if r == nil || r.before != 7*24*time.Hour || r.every != 12*time.Hour || r.onError == nil {
		t.Fatalf("defaults = %+v; want 7 days before expiry, checked every 12 hours", r)
	}
}
