package dcr_sdk

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aradilov/fastrpc"
	"github.com/google/uuid"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/internal/sdkutil"
	"github.com/mygaru/dcr-sdk/pkg/contract"
	"google.golang.org/protobuf/proto"

	"github.com/mygaru/dcr-sdk/pkg/client"
)

const MaximumSimultaneousConnections = 4

func getTestClient(t *testing.T) *client.ShardedClient {
	t.Helper()

	server := newTestRPCServer(t, testRPCServerConfig{})
	return newTestClient(server.addr, MaximumSimultaneousConnections)
}

func newTestClient(addr string, maximumSimultaneousConnections int) *client.ShardedClient {
	return New(&client.Configuration{
		Addrs:                          addr,
		JwtToken:                       []byte("some token here ..."),
		MaximumSimultaneousConnections: maximumSimultaneousConnections,
	})
}

func TestTargetReturnsTrackingIDAndReportRoutesByIt(t *testing.T) {
	server := newTestRPCServer(t, testRPCServerConfig{serverID: 1024})
	rpc := newTestClient(server.addr, 2)

	resp, sc, err := rpc.Target(&base.TargetRequest{
		Uids: []*base.UID{
			{Id: []byte(uuid.New().String()), Type: base.UID_DEVICE_ID},
		},
		Match: []*base.Match_Rule{
			{TrafficType: base.TrafficType_TRAFFIC_TYPE_VIDEO, SegmentIds: []uint32{1, 2, 3}},
		},
	})
	if nil != err {
		t.Fatalf("expected error to be %v, got %v", nil, err)
	}
	if sc != base.RPCServerResponseCode_OK {
		t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_OK, sc)
	}
	if len(resp.TrackingId) != 16 {
		t.Fatalf("expected 16-byte tracking id, got %q", resp.TrackingId)
	}
	if !rpc.IsValidTrackingID(resp.TrackingId) {
		t.Fatalf("expected tracking id %q to be valid for this client", resp.TrackingId)
	}

	report := &base.ReportRequest{
		TrackingId: resp.TrackingId,
		Event:      base.EventType_EVENT_TYPE_CLICK,
		Rules: []*base.ReportRequest_Rule{
			{
				TrafficType: base.TrafficType_TRAFFIC_TYPE_VIDEO,
				EventsCount: 1,
				SegmentIds:  []uint32{1, 2, 3},
			},
		},
	}
	sc, err = rpc.Report(report)
	if nil != err {
		t.Fatalf("expected nil, got err %v", err)
	}
	if sc != base.RPCServerResponseCode_OK {
		t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_OK, sc)
	}

	got := server.nextReport(t)
	if string(got.TrackingId) != string(report.TrackingId) {
		t.Fatalf("expected report tracking id %q, got %q", report.TrackingId, got.TrackingId)
	}
	if got.Event != report.Event {
		t.Fatalf("expected report event %s, got %s", report.Event, got.Event)
	}
}

func TestAuth(t *testing.T) {
	rpc := getTestClient(t)
	for i := 0; i < 100; i++ {
		_, sc, err := rpc.Mock(&base.MockRequest{StatusCode: base.RPCServerResponseCode_OK}, nil)
		if nil != err {
			t.Fatalf("expected nil, got err %v", err)
		}

		if sc != base.RPCServerResponseCode_OK {
			t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_OK, sc)
		}
	}

	if rpc.Reconnects() != MaximumSimultaneousConnections {
		t.Fatalf("expected %d reconnects, got %d", MaximumSimultaneousConnections, rpc.Reconnects())
	}
}

func TestFailed(t *testing.T) {
	_, sc, err := getTestClient(t).Mock(&base.MockRequest{StatusCode: base.RPCServerResponseCode_UNAUTHORIZED}, nil)
	if nil == err {
		t.Fatalf("expected error, got none")
	}

	if sc != base.RPCServerResponseCode_UNAUTHORIZED {
		t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_UNAUTHORIZED, sc)
	}
}

func TestAuthFailureReturnsUnauthorized(t *testing.T) {
	server := newTestRPCServer(t, testRPCServerConfig{
		authStatusCode: base.RPCServerResponseCode_UNAUTHORIZED,
	})
	rpc := newTestClient(server.addr, 1)

	_, sc, err := rpc.Mock(&base.MockRequest{StatusCode: base.RPCServerResponseCode_OK}, nil)
	if !errors.Is(err, client.ErrorUnauthorized) {
		t.Fatalf("expected unauthorized error, got %v", err)
	}
	if sc != base.RPCServerResponseCode_UNAUTHORIZED {
		t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_UNAUTHORIZED, sc)
	}
}

func TestTargetReturnsServerStatusError(t *testing.T) {
	server := newTestRPCServer(t, testRPCServerConfig{
		targetStatus: base.RPCServerResponseCode_SERVICE_UNAVAILABLE,
	})
	rpc := newTestClient(server.addr, 1)

	resp, sc, err := rpc.Target(&base.TargetRequest{
		Uids: []*base.UID{
			{Id: []byte(uuid.New().String()), Type: base.UID_DEVICE_ID},
		},
	})
	if nil == err {
		t.Fatalf("expected target error")
	}
	if resp != nil {
		t.Fatalf("expected nil target response, got %+v", resp)
	}
	if sc != base.RPCServerResponseCode_SERVICE_UNAVAILABLE {
		t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_SERVICE_UNAVAILABLE, sc)
	}
}

func TestReportRejectsMissingAndUnknownTrackingID(t *testing.T) {
	rpc := getTestClient(t)

	sc, err := rpc.Report(&base.ReportRequest{})
	if nil == err {
		t.Fatalf("expected missing tracking id error")
	}
	if sc != base.RPCServerResponseCode_UNKNOWN {
		t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_UNKNOWN, sc)
	}

	sc, err = rpc.Report(&base.ReportRequest{TrackingId: []byte("FFFF000000000001")})
	if nil == err {
		t.Fatalf("expected unknown server error")
	}
	if sc != base.RPCServerResponseCode_UNKNOWN {
		t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_UNKNOWN, sc)
	}
}

func TestNewPanicsOnNilConfiguration(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic")
		}
	}()
	New(nil)
}

func TestNewWithTLSPanicsOnNilTLSConfig(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic")
		}
	}()
	NewWithTLS(&client.Configuration{}, nil)
}

func TestNew(t *testing.T) {

	tests := []struct {
		name       string
		cfg        *client.Configuration
		expectAddr string
	}{
		{
			name:       "nil configuration",
			cfg:        &client.Configuration{},
			expectAddr: "cloud.mygaru.com:7937",
		},
		{
			name: "addr configuration",
			cfg: &client.Configuration{
				Addrs: "anyaddr.here:8080",
			},
			expectAddr: "anyaddr.here:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sdk := New(tt.cfg)
			if sdk.Configuration.Addrs != tt.expectAddr {
				t.Errorf("expected addrs to be %q, got %q", tt.expectAddr, sdk.Configuration.Addrs)
			}
		})
	}
}

type testRPCServerConfig struct {
	authStatusCode base.RPCServerResponseCode
	serverID       uint16
	targetStatus   base.RPCServerResponseCode
}

type testRPCServer struct {
	addr     string
	serverID uint16
	reports  chan *base.ReportRequest
	seq      atomic.Uint64

	authStatusCode base.RPCServerResponseCode
	targetStatus   base.RPCServerResponseCode
}

func newTestRPCServer(t *testing.T, cfg testRPCServerConfig) *testRPCServer {
	t.Helper()

	if cfg.authStatusCode == base.RPCServerResponseCode_UNKNOWN {
		cfg.authStatusCode = base.RPCServerResponseCode_OK
	}
	if cfg.serverID == 0 {
		cfg.serverID = 1024
	}
	if cfg.targetStatus == base.RPCServerResponseCode_UNKNOWN {
		cfg.targetStatus = base.RPCServerResponseCode_OK
	}

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot listen on test tcp addr: %v", err)
	}

	testServer := &testRPCServer{
		addr:           ln.Addr().String(),
		serverID:       cfg.serverID,
		reports:        make(chan *base.ReportRequest, 8),
		authStatusCode: cfg.authStatusCode,
		targetStatus:   cfg.targetStatus,
	}

	server := &fastrpc.Server{
		SniffHeader:     sdkutil.SniffHeader,
		ProtocolVersion: sdkutil.ProtocolVersion,
		Handler:         testServer.handle,
		NewHandlerCtx: func() fastrpc.HandlerCtx {
			return &contract.RequestCtx{}
		},
		ReadTimeout:      5 * time.Minute,
		WriteTimeout:     10 * time.Second,
		CompressType:     fastrpc.CompressSnappy,
		PipelineRequests: true,
	}

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(&testRPCListener{Listener: ln})
	}()

	t.Cleanup(func() {
		_ = ln.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("test rpc server stopped with error: %v", err)
			}
		case <-time.After(time.Second):
			t.Errorf("test rpc server did not stop in time")
		}
	})

	return testServer
}

func (s *testRPCServer) handle(ctxv fastrpc.HandlerCtx) fastrpc.HandlerCtx {
	ctx := ctxv.(*contract.RequestCtx)
	conn := ctx.Conn().(*testRPCConn)

	switch ctx.Request.GetName() {
	case contract.Auth:
		s.handleAuth(ctx, conn)
	case contract.Mock:
		s.handleMock(ctx)
	case contract.Target:
		s.handleTarget(ctx)
	case contract.Report:
		s.handleReport(ctx)
	default:
		writeTestRPCError(ctx, base.RPCServerResponseCode_INVALID_REQUEST, fmt.Errorf("unsupported request name: %s", ctx.Request.GetName()))
	}

	return ctxv
}

func (s *testRPCServer) handleAuth(ctx *contract.RequestCtx, conn *testRPCConn) {
	if s.authStatusCode != base.RPCServerResponseCode_OK {
		writeTestRPCError(ctx, s.authStatusCode, fmt.Errorf("unauthorized"))
		return
	}

	ctx.Response.SetStatusCode(base.RPCServerResponseCode_OK)
	conn.authID = uuid.New()

	buf := ctx.Response.SwapValue(nil)
	buf = binary.LittleEndian.AppendUint16(buf, s.serverID)
	buf = append(buf, conn.authID[:]...)
	ctx.Response.SwapValue(buf)
}

func (s *testRPCServer) handleMock(ctx *contract.RequestCtx) {
	req := &base.MockRequest{}
	if err := proto.Unmarshal(ctx.Request.Value(), req); err != nil {
		writeTestRPCError(ctx, base.RPCServerResponseCode_INVALID_REQUEST, fmt.Errorf("cannot unmarshal mock request: %w", err))
		return
	}

	if req.StatusCode > base.RPCServerResponseCode_OK {
		writeTestRPCError(ctx, req.StatusCode, fmt.Errorf("test rpc error"))
		return
	}

	resp := &base.TargetResponse{
		TrackingId: req.TrackingId,
	}
	writeTestRPCProto(ctx, base.RPCServerResponseCode_OK, resp)
}

func (s *testRPCServer) handleTarget(ctx *contract.RequestCtx) {
	req := &base.TargetRequest{}
	if err := proto.Unmarshal(ctx.Request.Value(), req); err != nil {
		writeTestRPCError(ctx, base.RPCServerResponseCode_INVALID_REQUEST, fmt.Errorf("cannot unmarshal target request: %w", err))
		return
	}

	if s.targetStatus != base.RPCServerResponseCode_OK {
		writeTestRPCError(ctx, s.targetStatus, fmt.Errorf("target failed"))
		return
	}

	resp := &base.TargetResponse{
		TrackingId: s.nextTrackingID(),
		StatusCode: base.RPCServerResponseCode_OK,
		Match:      make([]base.Match_ResponseStatus, len(req.Match)),
		Frequency:  make([]base.Frequency_ResponseStatus, len(req.Frequency)),
	}
	writeTestRPCProto(ctx, base.RPCServerResponseCode_OK, resp)
}

func (s *testRPCServer) handleReport(ctx *contract.RequestCtx) {
	req := &base.ReportRequest{}
	if err := proto.Unmarshal(ctx.Request.Value(), req); err != nil {
		writeTestRPCError(ctx, base.RPCServerResponseCode_INVALID_REQUEST, fmt.Errorf("cannot unmarshal report request: %w", err))
		return
	}

	s.reports <- req
	ctx.Response.SetStatusCode(base.RPCServerResponseCode_OK)
}

func (s *testRPCServer) nextTrackingID() []byte {
	n := s.seq.Add(1)
	return []byte(fmt.Sprintf("%04X%012X", s.serverID, n))
}

func (s *testRPCServer) nextReport(t *testing.T) *base.ReportRequest {
	t.Helper()

	select {
	case report := <-s.reports:
		return report
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for report request")
		return nil
	}
}

func writeTestRPCProto(ctx *contract.RequestCtx, statusCode base.RPCServerResponseCode, msg proto.Message) {
	bb, err := proto.Marshal(msg)
	if err != nil {
		writeTestRPCError(ctx, base.RPCServerResponseCode_TECH_ERROR, fmt.Errorf("cannot marshal response: %w", err))
		return
	}

	ctx.Response.SetStatusCode(statusCode)
	_, _ = ctx.Write(bb)
}

func writeTestRPCError(ctx *contract.RequestCtx, statusCode base.RPCServerResponseCode, err error) {
	ctx.Response.SetStatusCode(statusCode)
	_, _ = ctx.Write([]byte(err.Error()))
}

type testRPCConn struct {
	net.Conn
	authID uuid.UUID
}

type testRPCListener struct {
	net.Listener
}

func (ln *testRPCListener) Accept() (net.Conn, error) {
	conn, err := ln.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &testRPCConn{Conn: conn}, nil
}
