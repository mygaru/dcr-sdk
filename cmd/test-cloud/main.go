package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"sync/atomic"
	"time"

	"github.com/aradilov/fastrpc"
	"github.com/google/uuid"
	"github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/internal/sdkutil"
	"github.com/mygaru/dcr-sdk/pkg/contract"
	"github.com/mygaru/dcr-sdk/pkg/serverauth"
	"google.golang.org/protobuf/proto"
)

var (
	listenAddr      = flag.String("listenAddr", "127.0.0.1:7943", "TCP address for accepting test-cloud RPC requests")
	serverID        = flag.Uint("serverID", 1024, "server id encoded into generated tracking ids")
	tlsCertPath     = flag.String("tlsCert", "", "PEM-encoded TLS server certificate")
	tlsKeyPath      = flag.String("tlsKey", "", "PEM-encoded TLS server private key")
	clientCAPath    = flag.String("clientCA", "", "PEM-encoded CA used to verify mTLS client certificates")
	clientIssuer    = flag.String("clientIssuer", "", "PEM-encoded issuer certificate used for OCSP checks")
	requireOCSP     = flag.Bool("requireOCSP", false, "require good OCSP status for mTLS client certificates")
	trackingCounter atomic.Uint64
)

func main() {
	flag.Parse()

	ln, err := net.Listen("tcp4", *listenAddr)
	if err != nil {
		log.Fatalf("test-cloud: listen on %q: %v", *listenAddr, err)
	}
	ln = serverauth.NewListener(ln)

	tlsConfig, err := loadTLSConfig()
	if err != nil {
		log.Fatalf("test-cloud: load TLS config: %v", err)
	}

	server := &fastrpc.Server{
		SniffHeader:     sdkutil.SniffHeader,
		ProtocolVersion: sdkutil.ProtocolVersion,
		Handler:         handler,
		NewHandlerCtx: func() fastrpc.HandlerCtx {
			return &contract.RequestCtx{
				ConcurrencyLimitErrorHandler: func(ctx *contract.RequestCtx, concurrency int) {
					writeError(ctx, base.RPCServerResponseCode_TECH_ERROR, fmt.Errorf("concurrency limit exceeded: %d", concurrency))
				},
			}
		},
		ReadTimeout:      5 * time.Minute,
		WriteTimeout:     10 * time.Second,
		CompressType:     fastrpc.CompressSnappy,
		PipelineRequests: true,
		TLSConfig:        tlsConfig,
	}

	log.Printf("Starting test-cloud RPC server at %q", *listenAddr)
	if err := server.Serve(ln); err != nil {
		log.Fatalf("test-cloud: serve failed on %q: %v", *listenAddr, err)
	}
}

func loadTLSConfig() (*tls.Config, error) {
	if *tlsCertPath == "" && *tlsKeyPath == "" && *clientCAPath == "" {
		return nil, nil
	}
	if *tlsCertPath == "" || *tlsKeyPath == "" || *clientCAPath == "" {
		return nil, fmt.Errorf("tlsCert, tlsKey, and clientCA must be set together")
	}

	serverCert, err := tls.LoadX509KeyPair(*tlsCertPath, *tlsKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load server key pair: %w", err)
	}

	clientCAPEM, err := os.ReadFile(*clientCAPath)
	if err != nil {
		return nil, fmt.Errorf("read client CA: %w", err)
	}
	clientRoots := x509.NewCertPool()
	if !clientRoots.AppendCertsFromPEM(clientCAPEM) {
		return nil, fmt.Errorf("parse client CA")
	}

	var issuer *x509.Certificate
	if *clientIssuer != "" {
		issuer, err = readCertificate(*clientIssuer)
		if err != nil {
			return nil, fmt.Errorf("read client issuer: %w", err)
		}
	}

	// Example for real mTLS auth failure handling:
	// if unauthorized {
	// 	return nil, fmt.Errorf("unauthorized")
	// }
	// fastrpc closes the connection when the TLS handshake returns an error.

	return serverauth.NewTLSConfig(&tls.Config{
		Certificates: []tls.Certificate{serverCert},
	}, serverauth.MTLSConfig{
		Roots:       clientRoots,
		Issuer:      issuer,
		RequireOCSP: *requireOCSP,
	}), nil
}

func readCertificate(path string) (*x509.Certificate, error) {
	certPEM, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("decode certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

func handler(ctxv fastrpc.HandlerCtx) fastrpc.HandlerCtx {
	ctx := ctxv.(*contract.RequestCtx)

	switch ctx.Request.GetName() {
	case contract.Auth:
		handleAuth(ctx)
	case contract.Target:
		handleTarget(ctx)
	case contract.Report:
		handleReport(ctx)
	default:
		writeError(ctx, base.RPCServerResponseCode_INVALID_REQUEST, fmt.Errorf("unsupported request name: %s", ctx.Request.GetName()))
	}
	return ctxv
}

func handleAuth(ctx *contract.RequestCtx) {
	if _, ok := serverauth.GetUUID(ctx.Conn()); ok {
		writeError(ctx, base.RPCServerResponseCode_INVALID_REQUEST, fmt.Errorf("connection is already authenticated"))
		return
	}

	// Example for real auth failure handling:
	// if unauthorized {
	// 	writeError(ctx, base.RPCServerResponseCode_UNAUTHORIZED, fmt.Errorf("unauthorized"))
	// 	_ = ctx.Conn().Close()
	// 	return
	// }

	payerID, err := uuid.Parse(string(ctx.Request.Value()))
	if err != nil {
		writeError(ctx, base.RPCServerResponseCode_UNAUTHORIZED, fmt.Errorf("invalid test JWT UUID: %w", err))
		return
	}
	if err := serverauth.SetUUID(ctx.Conn(), payerID); err != nil {
		writeError(ctx, base.RPCServerResponseCode_TECH_ERROR, err)
		return
	}

	ctx.Response.SetStatusCode(base.RPCServerResponseCode_OK)
	buf := ctx.Response.SwapValue(nil)
	buf = binary.LittleEndian.AppendUint16(buf, uint16(*serverID))
	buf = append(buf, payerID[:]...)
	ctx.Response.SwapValue(buf)
}

func handleTarget(ctx *contract.RequestCtx) {
	if _, ok := serverauth.GetUUID(ctx.Conn()); !ok {
		writeError(ctx, base.RPCServerResponseCode_UNAUTHORIZED, fmt.Errorf("unauthorized"))
		return
	}

	req := &base.TargetRequest{}
	if err := proto.Unmarshal(ctx.Request.Value(), req); err != nil {
		writeError(ctx, base.RPCServerResponseCode_INVALID_REQUEST, fmt.Errorf("cannot unmarshal target request: %w", err))
		return
	}

	resp := &base.TargetResponse{
		TrackingId: nextTrackingID(),
		StatusCode: base.RPCServerResponseCode_OK,
		Match:      make([]base.Match_ResponseStatus, len(req.Match)),
		Frequency:  make([]base.Frequency_ResponseStatus, len(req.Frequency)),
	}
	for i := range resp.Match {
		resp.Match[i] = base.Match_OK
	}
	for i := range resp.Frequency {
		resp.Frequency[i] = base.Frequency_STATUS_PASSED
	}
	writeProto(ctx, base.RPCServerResponseCode_OK, resp)
}

func handleReport(ctx *contract.RequestCtx) {
	if _, ok := serverauth.GetUUID(ctx.Conn()); !ok {
		writeError(ctx, base.RPCServerResponseCode_UNAUTHORIZED, fmt.Errorf("unauthorized"))
		return
	}

	req := &base.ReportRequest{}
	if err := proto.Unmarshal(ctx.Request.Value(), req); err != nil {
		writeError(ctx, base.RPCServerResponseCode_INVALID_REQUEST, fmt.Errorf("cannot unmarshal report request: %w", err))
		return
	}

	ctx.Response.SetStatusCode(base.RPCServerResponseCode_OK)
}

func nextTrackingID() []byte {
	n := trackingCounter.Add(1)
	return []byte(fmt.Sprintf("%04X%012X", uint16(*serverID), n))
}

func writeProto(ctx *contract.RequestCtx, statusCode base.RPCServerResponseCode, msg proto.Message) {
	bb, err := proto.Marshal(msg)
	if err != nil {
		writeError(ctx, base.RPCServerResponseCode_TECH_ERROR, fmt.Errorf("cannot marshal response: %w", err))
		return
	}
	ctx.Response.SetStatusCode(statusCode)
	_, _ = ctx.Write(bb)
}

func writeError(ctx *contract.RequestCtx, statusCode base.RPCServerResponseCode, err error) {
	ctx.Response.SetStatusCode(statusCode)
	_, _ = ctx.Write([]byte(err.Error()))
}
