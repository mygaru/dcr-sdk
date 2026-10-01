// client-example shows how an application integrates the SDK and keeps its
// client certificate renewed:
//
//  1. it loads the certificate and key from the files its configuration names;
//  2. it creates the client with them, and with OnRenewed, which turns on the
//     SDK's automatic renewal: a week before the certificate expires the SDK
//     renews it at the myGaru CA, OnRenewed (dcr.SaveToFiles) saves the new
//     certificate and key over the files, and the SDK switches its
//     connections to it.
//
// Run it with the certificate and key the manager or the member zone provided:
//
//	go run ./cmd/client-example -cert client.pem -key client-key.pem
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	dcr "github.com/mygaru/dcr-sdk"
	"github.com/mygaru/dcr-sdk/pkg/client"
)

var (
	certPath    = flag.String("cert", "", "PEM client certificate, intermediates may follow (required)")
	keyPath     = flag.String("key", "", "PEM private key of -cert (required)")
	addr        = flag.String("addr", "cloud.mygaru.com:7937", "Comma-separated DCR cloud RPC addresses")
	caURL       = flag.String("caURL", dcr.DefaultCAURL, "myGaru CA the certificate is renewed at")
	renewBefore = flag.Duration("renewBefore", 7*24*time.Hour, "Renew the certificate this long before it expires")
)

func main() {
	flag.Parse()

	if *certPath == "" || *keyPath == "" {
		log.Fatal("-cert and -key are required")
	}

	// Step 1: load the certificate and key from the files.
	certPEM, err := os.ReadFile(*certPath)
	if err != nil {
		log.Fatalf("read certificate: %v", err)
	}
	keyPEM, err := os.ReadFile(*keyPath)
	if err != nil {
		log.Fatalf("read key: %v", err)
	}

	// Step 2: create the client. The server certificate is a public (ACME)
	// one, checked against the system roots; the client certificate against
	// the myGaru CA the SDK carries.
	serverName, _, err := net.SplitHostPort(*addr)
	if err != nil {
		log.Fatalf("-addr %q: %v", *addr, err)
	}
	cli, err := dcr.NewWithMTLS(&client.Configuration{
		Addrs:              *addr,
		MaxRequestDuration: time.Second,
	}, dcr.MTLSConfig{
		CertPEM:    certPEM,
		KeyPEM:     keyPEM,
		ServerName: serverName,

		// automatic renewal
		CAURL:       *caURL,
		RenewBefore: *renewBefore,
		// saves the renewed certificate over the files loaded above
		OnRenewed: dcr.SaveToFiles(*certPath, *keyPath),
		OnRenewError: func(err error) {
			// the current certificate stays in use; the SDK tries again
			log.Printf("certificate renewal failed: %v", err)
		},
	})
	if err != nil {
		log.Fatalf("create client: %v", err)
	}

	leaf := cli.CertProvider().Leaf()
	log.Printf("client ready: %s, certificate %s valid until %s, renewed from %s",
		*addr, leaf.SerialNumber, leaf.NotAfter.Format(time.RFC3339), leaf.NotAfter.Add(-*renewBefore).Format(time.RFC3339))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The application's own work goes here: cli.Target, cli.Report, ...
	<-ctx.Done()
}
