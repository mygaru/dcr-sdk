package client

import (
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"github.com/aradilov/fastrpc"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/pkg/contract"
	"google.golang.org/protobuf/proto"
)

type client struct {
	// maxRequestDuration specifies the maximum duration allowed for a single request to complete before timing out.
	maxRequestDuration time.Duration

	// metricGroups is an array of metricsGroup pointers, indexed by request identifiers, for tracking metrics of RPC calls.
	metricGroups [contract.MaxRequestIdentifier + 1]*metricsGroup

	// single connection RPC client
	c *fastrpc.Client

	// connDials counts successfully established connections, i.e. the number of
	// (re)connects this client has performed.
	connDials atomic.Uint64

	// conn is the connection fastrpc is serving on, for reconnect to close.
	conn atomic.Pointer[dialedConn]
}

// dialedConn is a connection the client dialed.
type dialedConn struct {
	net.Conn
}

// onConnDialed is called from the fastrpc Dial hook once a connection is
// established.
func (c *client) onConnDialed(conn net.Conn) {
	c.connDials.Add(1)
	c.conn.Store(&dialedConn{Conn: conn})
}

// reconnect closes the client's connection, so fastrpc dials a new one -
// handshaking with the current certificate. Unless force, it first waits, at
// most wait, for the requests in flight on it to finish.
func (c *client) reconnect(force bool, wait time.Duration) {
	conn := c.conn.Load()
	if conn == nil {
		return
	}

	if !force {
		deadline := time.Now().Add(wait)
		for c.c.PendingRequests() > 0 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
	}

	_ = conn.Close()
	c.conn.CompareAndSwap(conn, nil)
}

// Reconnects returns the number of connections the client has established.
func (c *client) Reconnects() uint64 {
	return c.connDials.Load()
}

// doUnary sends a unary RPC request with a given proto message and request identifier.
//
// Timing model: the call goes straight to fastrpc.Client.DoDeadline with
// deadline = now + maxRequestDuration. fastrpc does not enforce this deadline
// with a per-request timer; it relies on an internal stale-request checker.
// Because of that, the observed call duration may exceed maxRequestDuration by
// up to the checker wake-up delay (currently up to about 1s in fastrpc). A
// request that arrives while the connection is being (re)established waits for
// the dial and the fastrpc protocol handshake, which has its own 3s deadline
// inside fastrpc.
//
// Requests carry the payer themselves, so there is no connection state to
// establish first and no ordering requirement between requests on a connection.
func (c *client) doUnary(req, resp proto.Message, reqn contract.RPCRegister) (proto.Message, base.RPCServerResponseCode, error) {
	raw, err := proto.Marshal(req)
	if nil != err {
		return nil, base.RPCServerResponseCode_UNKNOWN, fmt.Errorf("marshal request %s is failed: %+v", reqn, err)
	}

	if 0 == len(raw) {
		return nil, base.RPCServerResponseCode_UNKNOWN, fmt.Errorf("marshal request %s is failed: empty request", reqn)
	}

	metricGroup := c.metricGroups[reqn]

	st := time.Now()
	rpcReq := contract.AcquireRequest()
	rpcResp := contract.AcquireResponse()
	defer func() {
		contract.ReleaseRequest(rpcReq)
		contract.ReleaseResponse(rpcResp)
	}()

	rpcReq.SetName(reqn)
	rpcReq.Append(raw)

	metricGroup.request.Inc()
	err = c.c.DoDeadline(rpcReq, rpcResp, st.Add(c.maxRequestDuration))
	metricGroup.duration.UpdateDuration(st)
	if err != nil {
		c.countError(reqn, err, rpcResp)
		return nil, base.RPCServerResponseCode_NETWORK_ERROR, fmt.Errorf("error when calling '%s': %s", reqn, err)
	}

	statusCode := rpcResp.GetStatusCode()
	if statusCode != base.RPCServerResponseCode_OK {
		err = fmt.Errorf("RPC[%q]: %s", statusCode.String(), rpcResp.Value())
		c.countError(reqn, nil, rpcResp)
		return nil, statusCode, err
	}

	if nil != resp {
		err = proto.Unmarshal(rpcResp.Value(), resp)
	}

	if nil != err {
		return nil, statusCode, fmt.Errorf("unmarshal response of request '%s' is failed: %w", reqn, err)
	}
	metricGroup.success.Inc()
	return resp, statusCode, nil
}

// countError increments error-related metrics for the given request and logs unhandled errors with request and server details.
func (c *client) countError(reqn contract.RPCRegister, err error, resp *contract.Response) {
	metricGroup := c.metricGroups[reqn]

	if nil == err {
		metricGroup.failed.Inc()
		return
	}

	switch {
	case errors.Is(err, fastrpc.ErrTimeout):
		metricGroup.timeout.Inc()
	case errors.Is(err, fastrpc.ErrPendingRequestsOverflow):
		metricGroup.overflow.Inc()
	default:
		metricGroup.error.Inc()
	}
}
