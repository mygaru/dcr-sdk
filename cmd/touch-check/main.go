// touch-check sends one Touch to the DCR cloud over mTLS and prints what comes
// back: a quick way to check a client certificate and its Touch grant.
//
//	go run ./cmd/touch-check -cert client.pem -key client-key.pem -deviceID 019d2555-...
//
// For internal use, like Touch itself.
package main

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"gitlab.adtelligent.com/awesome/mtls"
	dcr "github.com/mygaru/dcr-sdk"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/pkg/client"
)

var (
	addr       = flag.String("addr", "cloud.mygaru.com:7937", "DCR cloud RPC address")
	certPath   = flag.String("cert", "", "PEM client certificate, intermediates may follow (required)")
	keyPath    = flag.String("key", "", "PEM private key of -cert (required)")
	serverName = flag.String("serverName", "", "Name the server certificate is verified for; the host of -addr when empty")
	serverCA   = flag.String("serverCA", "", "PEM CA the server certificate is verified against; the system roots when empty")
	clientCA   = flag.String("clientCA", "", "PEM CA the client certificate is checked against before dialing; the myGaru CA the SDK carries when empty")

	deviceID          = flag.String("deviceID", "", "Device id to identify the user by")
	externalUID       = flag.String("externalUID", "", "External uid to identify the user by")
	externalUIDSource = flag.String("externalUIDSource", "", "Source (id space) of -externalUID; adtelligent.com when empty")
	otp               = flag.String("otp", "", "OTP to identify the user by")
	partnerUID        = flag.String("partnerUID", "", "Partner uid (puid) to identify the user by")

	timeout = flag.Duration("timeout", 5*time.Second, "Request and dial timeout")
)

func main() {
	flag.Parse()

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "touch-check: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if *certPath == "" || *keyPath == "" {
		return errors.New("-cert and -key are required")
	}

	uids := requestUIDs()
	if len(uids) == 0 {
		return errors.New("name at least one identifier: -deviceID, -externalUID, -otp or -partnerUID")
	}

	certPEM, err := os.ReadFile(*certPath)
	if err != nil {
		return err
	}
	keyPEM, err := os.ReadFile(*keyPath)
	if err != nil {
		return err
	}
	describeCertificate(certPEM)

	name := *serverName
	if name == "" {
		host, _, err := net.SplitHostPort(*addr)
		if err != nil {
			return fmt.Errorf("-addr %q: %w", *addr, err)
		}
		name = host
	}

	cfg := dcr.MTLSConfig{CertPEM: certPEM, KeyPEM: keyPEM, ServerName: name}
	if cfg.ServerRootCAs, err = loadPool(*serverCA); err != nil {
		return fmt.Errorf("-serverCA: %w", err)
	}
	if cfg.ClientCertRoots, err = loadPool(*clientCA); err != nil {
		return fmt.Errorf("-clientCA: %w", err)
	}

	cli, err := dcr.NewWithMTLS(&client.Configuration{
		Addrs:                          *addr,
		MaximumSimultaneousConnections: 1,
		MaxRequestDuration:             *timeout,
		MaxDialDuration:                *timeout,
	}, cfg)
	if err != nil {
		return fmt.Errorf("client certificate refused before dialing (pass the issuing CA in -clientCA if it is not the myGaru CA): %w", err)
	}

	fmt.Printf("Touch -> %s (server name %s)\n", *addr, name)
	start := time.Now()
	resp, status, err := cli.Touch(&base.TouchRequest{Uids: uids})
	took := time.Since(start).Round(time.Millisecond)
	if err != nil {
		return fmt.Errorf("status=%s after %s: %w", status, took, err)
	}

	fmt.Printf("status:   %s (%s)\n", status, took)
	fmt.Printf("user_id:  %s\n", orDash(resp.GetUserId()))
	fmt.Printf("accuracy: %s\n", resp.GetAccuracy())
	fmt.Printf("label:    %s\n", orDash(resp.GetLabel()))
	return nil
}

func requestUIDs() []*base.UID {
	var uids []*base.UID
	if *deviceID != "" {
		uids = append(uids, &base.UID{Id: []byte(*deviceID), Type: base.UID_DEVICE_ID})
	}
	if *externalUID != "" {
		uids = append(uids, &base.UID{Id: []byte(*externalUID), Type: base.UID_EXTERNAL_UID, Source: *externalUIDSource})
	}
	if *otp != "" {
		uids = append(uids, &base.UID{Id: []byte(*otp), Type: base.UID_OTP})
	}
	if *partnerUID != "" {
		uids = append(uids, &base.UID{Id: []byte(*partnerUID), Type: base.UID_PARTNER_UID})
	}
	return uids
}

// describeCertificate prints what the cloud reads off the client certificate:
// the CN Touch is granted by, the partner it names, who issued it and until
// when.
func describeCertificate(certPEM []byte) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return
	}

	partner := mtls.GetUUID(cert)
	if partner == "" {
		partner = cert.Subject.CommonName + " (from the CN)"
	}

	fmt.Printf("certificate: CN=%s\n", cert.Subject.CommonName)
	fmt.Printf("  partner:   %s\n", partner)
	fmt.Printf("  issuer:    %s\n", cert.Issuer.CommonName)
	fmt.Printf("  serial:    %s\n", cert.SerialNumber)
	fmt.Printf("  valid:     %s .. %s\n", cert.NotBefore.Format(time.RFC3339), cert.NotAfter.Format(time.RFC3339))
}

func loadPool(path string) (*x509.CertPool, error) {
	if path == "" {
		return nil, nil
	}
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("%s holds no PEM certificate", path)
	}
	return pool, nil
}

func orDash(b []byte) string {
	if len(b) == 0 {
		return "-"
	}
	return string(b)
}
