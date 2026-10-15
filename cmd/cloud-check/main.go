// cloud-check verifies a DCR cloud deployment from the outside, the way a
// client sees it: it sends one Target, one Report and one Touch over each
// transport and reports whether the cloud took them.
//
//	CLOUD_CHECK_TOKEN=... go run ./cmd/cloud-check -cert client.pem -key client-key.pem -addr dcr-w1-nl.mygaru.com:7937
//
// RPC goes over mTLS with -cert/-key; a check passes when the cloud's handler
// answers, whatever it answers. HTTP goes to the same host over HTTPS with the
// OAuth token from CLOUD_CHECK_TOKEN or -tokenFile, which is what that
// transport authenticates by.
//
// The requests are built to leave nothing behind but statistics: the user is a
// random device id, so no OTP is decrypted, and the Report's rule names no
// segment, so no billing row is written.
//
// The exit code is 1 when a check failed (with -strict, also when one warned),
// so the command can gate a deployment.
package main

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"gitlab.adtelligent.com/awesome/mtls"

	dcr "github.com/mygaru/dcr-sdk"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/pkg/client"
)

// tokenEnv is where the OAuth token is read from when -tokenFile is not given:
// a token on the command line would end up in the shell history and in ps.
const tokenEnv = "CLOUD_CHECK_TOKEN"

var (
	addr       = flag.String("addr", "cloud.mygaru.com:7937", "DCR cloud RPC address")
	certPath   = flag.String("cert", "", "PEM client certificate, intermediates may follow (required for the RPC checks)")
	keyPath    = flag.String("key", "", "PEM private key of -cert")
	serverName = flag.String("serverName", "", "Name the RPC server certificate is verified for; the host of -addr when empty")
	serverCA   = flag.String("serverCA", "", "PEM CA the RPC server certificate is verified against; the system roots when empty")
	clientCA   = flag.String("clientCA", "", "PEM CA the client certificate is checked against before dialing; the myGaru CA the SDK carries when empty")
	payer      = flag.String("payer", "", "Payer of the RPC Report; the partner named in -cert when empty")

	httpURL        = flag.String("httpURL", "", "Base URL of the HTTP transport; https:// and the host of -addr when empty")
	tokenFile      = flag.String("tokenFile", "", "File holding the OAuth token of the HTTP checks; $"+tokenEnv+" when empty")
	touchSegmentID = flag.Uint("touchSegmentID", 0, "Segment of the HTTP touch; with 0 the cloud answers that there is no such segment, which still proves the route and the token")

	deviceID = flag.String("deviceID", "", "Device id the requests identify the user by; a random one when empty")

	skipRPC  = flag.Bool("skipRPC", false, "Do not run the RPC checks")
	skipHTTP = flag.Bool("skipHTTP", false, "Do not run the HTTP checks")
	timeout  = flag.Duration("timeout", 5*time.Second, "Dial and request timeout")
	jsonOut  = flag.Bool("json", false, "Print the results as JSON instead of a table")
	strict   = flag.Bool("strict", false, "Exit 1 on a warning as well as on a failure")
)

func main() {
	flag.Parse()

	if *deviceID == "" {
		*deviceID = uuid.NewString()
	}

	var results []Result

	if *skipRPC {
		results = append(results, Result{Name: "rpc", Outcome: Skip, Detail: "-skipRPC"})
	} else {
		results = append(results, rpcChecks()...)
	}

	if *skipHTTP {
		results = append(results, Result{Name: "http", Outcome: Skip, Detail: "-skipHTTP"})
	} else {
		results = append(results, httpChecks()...)
	}

	if *jsonOut {
		if err := printJSON(os.Stdout, results); err != nil {
			fmt.Fprintf(os.Stderr, "cloud-check: %v\n", err)
			os.Exit(2)
		}
	} else {
		printTable(os.Stdout, results)
	}

	os.Exit(exitCode(results, *strict))
}

func rpcChecks() []Result {
	cli, reqPayer, err := newRPCClient()
	if err != nil {
		return []Result{{Name: "rpc.client", Outcome: Fail, Detail: err.Error()}}
	}

	uid := &base.UID{Id: []byte(*deviceID), Type: base.UID_DEVICE_ID}

	return runRPCChecks(cli, reqPayer, uid)
}

// newRPCClient builds the mTLS client and works out the Report's payer.
//
// The client is checked against -clientCA before anything is dialed, so a
// certificate the cloud would refuse fails here, with the reason, rather than
// as a closed connection.
func newRPCClient() (*client.ShardedClient, string, error) {
	if *certPath == "" || *keyPath == "" {
		return nil, "", errors.New("-cert and -key are required (or -skipRPC)")
	}

	certPEM, err := os.ReadFile(*certPath)
	if err != nil {
		return nil, "", err
	}
	keyPEM, err := os.ReadFile(*keyPath)
	if err != nil {
		return nil, "", err
	}

	leaf, err := leafCertificate(certPEM)
	if err != nil {
		return nil, "", fmt.Errorf("-cert: %w", err)
	}
	if time.Now().After(leaf.NotAfter) {
		return nil, "", fmt.Errorf("-cert expired at %s", leaf.NotAfter.Format(time.RFC3339))
	}

	reqPayer := *payer
	if reqPayer == "" {
		reqPayer = mtls.GetUUID(leaf)
	}

	name := *serverName
	if name == "" {
		if name, err = hostOf(*addr); err != nil {
			return nil, "", err
		}
	}

	cfg := dcr.MTLSConfig{CertPEM: certPEM, KeyPEM: keyPEM, ServerName: name}
	if cfg.ServerRootCAs, err = loadPool(*serverCA); err != nil {
		return nil, "", fmt.Errorf("-serverCA: %w", err)
	}
	if cfg.ClientCertRoots, err = loadPool(*clientCA); err != nil {
		return nil, "", fmt.Errorf("-clientCA: %w", err)
	}

	cli, err := dcr.NewWithMTLS(&client.Configuration{
		Addrs:                          *addr,
		MaximumSimultaneousConnections: 1,
		MaxRequestDuration:             *timeout,
		MaxDialDuration:                *timeout,
	}, cfg)
	if err != nil {
		return nil, "", fmt.Errorf("client certificate refused before dialing (pass the issuing CA in -clientCA if it is not the myGaru CA): %w", err)
	}

	return cli, reqPayer, nil
}

func httpChecks() []Result {
	token, err := readToken()
	if err != nil {
		return []Result{{Name: "http.token", Outcome: Fail, Detail: err.Error()}}
	}

	baseURL := strings.TrimRight(*httpURL, "/")
	if baseURL == "" {
		host, err := hostOf(*addr)
		if err != nil {
			return []Result{{Name: "http.url", Outcome: Fail, Detail: err.Error()}}
		}
		baseURL = "https://" + host
	}

	c := &httpChecker{
		client: &http.Client{
			Timeout: *timeout,
			// A redirect would take the token somewhere else; the cloud never
			// redirects these routes, so one is answered as it is.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		baseURL: baseURL,
		token:   token,
	}

	return runHTTPChecks(c, *deviceID, *touchSegmentID)
}

func readToken() (string, error) {
	if *tokenFile != "" {
		b, err := os.ReadFile(*tokenFile)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}

	if token := strings.TrimSpace(os.Getenv(tokenEnv)); token != "" {
		return token, nil
	}

	return "", fmt.Errorf("no OAuth token: set $%s or -tokenFile (or -skipHTTP)", tokenEnv)
}

func hostOf(hostPort string) (string, error) {
	host, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		return "", fmt.Errorf("-addr %q: %w", hostPort, err)
	}
	return host, nil
}

func leafCertificate(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, errors.New("no PEM certificate")
	}
	return x509.ParseCertificate(block.Bytes)
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
