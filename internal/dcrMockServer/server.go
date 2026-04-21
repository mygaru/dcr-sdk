package dcrMockServer

import (
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"net"
	"time"
	"unsafe"

	"github.com/google/uuid"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/internal/sdkutil"
	"github.com/mygaru/dcr-sdk/pkg/contract"
	"google.golang.org/protobuf/proto"

	"github.com/aradilov/fastrpc"
)

var (
	ListenAddr = flag.String("ListenAddr", "127.0.0.1:7943",
		"TCP address for accepting RPC requests from dcrMock client")
)

// Init initializes dcrMockServer.
//
// Init must be called after flag.Parse() and before calling other
// dcrMockServer API.
func Init() {

	ln, err := net.Listen("tcp4", *ListenAddr)
	if err != nil {
		log.Fatal(fmt.Sprintf("dcrMockServer: cannot listen %q: %s", *ListenAddr, err))
	}

	server := &fastrpc.Server{
		SniffHeader:     sdkutil.SniffHeader,
		ProtocolVersion: sdkutil.ProtocolVersion,

		Handler: dcrMockHandler,
		NewHandlerCtx: func() fastrpc.HandlerCtx {
			return &contract.RequestCtx{
				ConcurrencyLimitErrorHandler: func(ctx *contract.RequestCtx, concurrency int) {
					fmt.Fprintf(ctx, "concurrency limit exceeded")
				},
			}
		},
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 10 * time.Second,

		CompressType:     fastrpc.CompressSnappy,
		PipelineRequests: true,
	}

	log.Printf("starting dcrMockServer at %q", *ListenAddr)
	go func() {
		if err := server.Serve(&customListener{ln}); err != nil {
			log.Fatal(fmt.Sprintf("dcrMockServer: error when listening %q: %s", *ListenAddr, err))
		}
	}()
}

func dcrMockHandler(ctxv fastrpc.HandlerCtx) fastrpc.HandlerCtx {
	ctx := ctxv.(*contract.RequestCtx)

	authConn := ctx.Conn().(*authConn)

	//if authConn.GetUUID() == uuid.Nil {
	//	writeError(ctx, base.RPCServerResponseCode_UNAUTHORIZED, fmt.Errorf("unauthorized"))
	//	return ctxv
	//}

	reqn := ctx.Request.GetName()
	switch reqn {
	case contract.Target:
	//target handler here ...
	case contract.Auth:
		ctx.Logger().Printf("Received auth token: %q", ctx.Request.Value())
		ctx.Response.SetStatusCode(base.RPCServerResponseCode_OK)
		authConn.SetUUID(uuid.New())
		buf := ctx.Response.SwapValue(nil)
		buf = binary.LittleEndian.AppendUint16(buf, 1024)
		payer := uuid.New()
		buf = append(buf, payer[:]...)
		ctx.Response.SwapValue(buf)
	case contract.Mock:
		mockTarget(ctx, authConn)
	default:
		ctx.Logger().Printf("Unsupported request name: %q", reqn)
	}

	return ctxv
}

func mockTarget(ctx *contract.RequestCtx, conn *authConn) {

	req := &base.MockRequest{}
	if err := proto.Unmarshal(ctx.Request.Value(), req); nil != err {
		writeError(ctx, base.RPCServerResponseCode_INVALID_REQUEST, fmt.Errorf("cannot unmarshal request: %w", err))
		return
	}

	if req.StatusCode > base.RPCServerResponseCode_OK {
		writeError(ctx, req.StatusCode, fmt.Errorf("any error description here"))
		return
	}

	ctx.Logger().Printf("Received mock request[%s]: %q", conn.GetUUID().String(), req.TrackingId)

	resp := &base.TargetResponse{
		TrackingId: req.TrackingId,
	}

	bb, err := proto.Marshal(resp)
	if nil != err {
		writeError(ctx, base.RPCServerResponseCode_TECH_ERROR, fmt.Errorf("cannot marshal response: %w", err))
		return
	}

	ctx.Response.SetStatusCode(base.RPCServerResponseCode_OK)
	_, _ = ctx.Write(bb)

}

func writeError(ctx *contract.RequestCtx, statusCode base.RPCServerResponseCode, err error) {
	ctx.Response.SetStatusCode(statusCode)

	s := err.Error()
	bb := unsafe.Slice(unsafe.StringData(s), len(s))
	_, _ = ctx.Write(bb)

	ctx.Logger().Printf(fmt.Sprintf("error when processing request: statusCode = %d, err = %s", statusCode, err))

}
