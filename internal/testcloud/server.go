package testcloud

import (
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"github.com/aradilov/fastrpc"
	"github.com/google/uuid"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/internal/sdkutil"
	"github.com/mygaru/dcr-sdk/pkg/contract"
	"github.com/mygaru/dcr-sdk/pkg/serverauth"
	"google.golang.org/protobuf/proto"
)

// Config controls a test-cloud RPC server instance.
type Config struct {
	// ListenAddr is used by ListenAndServe and Start. Start defaults to 127.0.0.1:0.
	ListenAddr string
	// ServerID is encoded into generated tracking IDs and auth responses. Defaults to 1024.
	ServerID uint16
	// TLSConfig enables fastrpc TLS/mTLS support. Nil keeps the server plaintext-only.
	TLSConfig *tls.Config
	// AuthStatusCode lets tests force contract.Auth failures. UNKNOWN means OK.
	AuthStatusCode base.RPCServerResponseCode
	// TargetStatus lets tests force Target failures. UNKNOWN means OK.
	TargetStatus base.RPCServerResponseCode
	// ReportBuffer controls how many Report requests are retained for tests. Defaults to 8.
	ReportBuffer int
}

// Server is an in-process test-cloud RPC server.
type Server struct {
	cfg     Config
	ln      net.Listener
	rpc     *fastrpc.Server
	done    chan error
	reports chan *base.ReportRequest
	counter atomic.Uint64
}

// Start starts a test-cloud RPC server in a goroutine.
func Start(cfg Config) (*Server, error) {
	cfg = normalizeConfig(cfg)
	ln, err := net.Listen("tcp4", cfg.ListenAddr)
	if err != nil {
		return nil, fmt.Errorf("listen on %q: %w", cfg.ListenAddr, err)
	}

	server := NewServer(cfg)
	server.ln = serverauth.NewListener(ln)
	server.done = make(chan error, 1)
	go func() {
		server.done <- server.rpc.Serve(server.ln)
	}()
	return server, nil
}

// ListenAndServe runs a test-cloud RPC server until the listener is closed.
func ListenAndServe(cfg Config) error {
	cfg = normalizeConfig(cfg)
	ln, err := net.Listen("tcp4", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen on %q: %w", cfg.ListenAddr, err)
	}
	return NewServer(cfg).Serve(ln)
}

// NewServer creates a test-cloud RPC server without starting it.
func NewServer(cfg Config) *Server {
	cfg = normalizeConfig(cfg)
	server := &Server{
		cfg:     cfg,
		reports: make(chan *base.ReportRequest, cfg.ReportBuffer),
	}
	server.rpc = &fastrpc.Server{
		SniffHeader:     sdkutil.SniffHeader,
		ProtocolVersion: sdkutil.ProtocolVersion,
		Handler:         server.handle,
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
		TLSConfig:        cfg.TLSConfig,
	}
	return server
}

// Serve serves requests from ln.
func (s *Server) Serve(ln net.Listener) error {
	return s.rpc.Serve(serverauth.NewListener(ln))
}

// Addr returns the server listener address.
func (s *Server) Addr() string {
	if s == nil || s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Reports returns Report requests accepted by the server.
func (s *Server) Reports() <-chan *base.ReportRequest {
	return s.reports
}

// Close stops a server started with Start.
func (s *Server) Close() error {
	if s == nil || s.ln == nil {
		return nil
	}
	if err := s.ln.Close(); err != nil {
		return err
	}
	if s.done == nil {
		return nil
	}
	select {
	case err := <-s.done:
		return err
	case <-time.After(time.Second):
		return fmt.Errorf("test-cloud server did not stop in time")
	}
}

func normalizeConfig(cfg Config) Config {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "127.0.0.1:0"
	}
	if cfg.ServerID == 0 {
		cfg.ServerID = 1024
	}
	if cfg.AuthStatusCode == base.RPCServerResponseCode_UNKNOWN {
		cfg.AuthStatusCode = base.RPCServerResponseCode_OK
	}
	if cfg.TargetStatus == base.RPCServerResponseCode_UNKNOWN {
		cfg.TargetStatus = base.RPCServerResponseCode_OK
	}
	if cfg.ReportBuffer <= 0 {
		cfg.ReportBuffer = 8
	}
	return cfg
}

func (s *Server) handle(ctxv fastrpc.HandlerCtx) fastrpc.HandlerCtx {
	ctx := ctxv.(*contract.RequestCtx)

	switch ctx.Request.GetName() {
	case contract.Auth:
		s.handleAuth(ctx)
	case contract.Target:
		s.handleTarget(ctx)
	case contract.Report:
		s.handleReport(ctx)
	default:
		writeError(ctx, base.RPCServerResponseCode_INVALID_REQUEST, fmt.Errorf("unsupported request name: %s", ctx.Request.GetName()))
	}
	return ctxv
}

func (s *Server) handleAuth(ctx *contract.RequestCtx) {
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

	if s.cfg.AuthStatusCode != base.RPCServerResponseCode_OK {
		writeError(ctx, s.cfg.AuthStatusCode, fmt.Errorf("unauthorized"))
		return
	}

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
	buf = binary.LittleEndian.AppendUint16(buf, s.cfg.ServerID)
	buf = append(buf, payerID[:]...)
	ctx.Response.SwapValue(buf)
}

func (s *Server) handleTarget(ctx *contract.RequestCtx) {
	if _, ok := serverauth.GetUUID(ctx.Conn()); !ok {
		writeError(ctx, base.RPCServerResponseCode_UNAUTHORIZED, fmt.Errorf("unauthorized"))
		return
	}

	req := &base.TargetRequest{}
	if err := proto.Unmarshal(ctx.Request.Value(), req); err != nil {
		writeError(ctx, base.RPCServerResponseCode_INVALID_REQUEST, fmt.Errorf("cannot unmarshal target request: %w", err))
		return
	}

	if s.cfg.TargetStatus != base.RPCServerResponseCode_OK {
		writeError(ctx, s.cfg.TargetStatus, fmt.Errorf("target failed"))
		return
	}

	resp := &base.TargetResponse{
		TrackingId: s.nextTrackingID(),
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

func (s *Server) handleReport(ctx *contract.RequestCtx) {
	if _, ok := serverauth.GetUUID(ctx.Conn()); !ok {
		writeError(ctx, base.RPCServerResponseCode_UNAUTHORIZED, fmt.Errorf("unauthorized"))
		return
	}

	req := &base.ReportRequest{}
	if err := proto.Unmarshal(ctx.Request.Value(), req); err != nil {
		writeError(ctx, base.RPCServerResponseCode_INVALID_REQUEST, fmt.Errorf("cannot unmarshal report request: %w", err))
		return
	}

	select {
	case s.reports <- req:
	default:
	}
	ctx.Response.SetStatusCode(base.RPCServerResponseCode_OK)
}

func (s *Server) nextTrackingID() []byte {
	n := s.counter.Add(1)
	return []byte(fmt.Sprintf("%04X%012X", s.cfg.ServerID, n))
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
