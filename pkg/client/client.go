package client

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aradilov/fastrpc"
	"github.com/google/uuid"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/pkg/contract"
	"google.golang.org/protobuf/proto"
)

var ErrorUnauthorized = errors.New("unauthorized")

type client struct {

	// JwtToken is a byte slice used for storing JSON Web Token (JWT) data associated with the client.
	JwtToken []byte

	// disableAuth skips the legacy contract.Auth request for mTLS-authenticated connections.
	disableAuth bool

	// maxRequestDuration specifies the maximum duration allowed for a single request to complete before timing out.
	maxRequestDuration time.Duration

	// metricGroups is an array of metricsGroup pointers, indexed by request identifiers, for tracking metrics of RPC calls.
	metricGroups [contract.MaxRequestIdentifier + 1]*metricsGroup

	// single connection RPC client
	c *fastrpc.Client

	// connDials counts successfully established connections. It doubles as the
	// identity of the current connection: the server binds the payer identity to
	// the TCP connection during Auth, so an Auth is only valid for the dial
	// generation it was performed on.
	connDials atomic.Uint64

	// connCloses counts closed connections. The connection is live only while
	// connDials > connCloses. Without this counter a dropped connection stays
	// invisible to the SDK (connDials only moves on the next dial), so the first
	// request after a disconnect skips Auth and is written by fastrpc to a fresh,
	// unauthenticated connection, which the server rejects with UNAUTHORIZED
	// "payer identity is missing".
	connCloses atomic.Uint64

	mu sync.Mutex

	authedDials atomic.Uint64
	authId      uuid.UUID
	serverID    uint16
}

// onConnDialed is called from the fastrpc Dial hook once a connection is
// established. It starts a new connection generation, which invalidates the
// previous Auth.
func (c *client) onConnDialed() {
	c.connDials.Add(1)
}

// onConnClosed is called from trackedConn.Close before the socket is closed, so
// the drop becomes visible to isAuthForCurrentConn before fastrpc re-dials.
func (c *client) onConnClosed() {
	c.connCloses.Add(1)
}

// invalidateAuth drops the authenticated-connection marker so that the next call
// performs Auth again. Dial generations start at 1, so zero never matches.
func (c *client) invalidateAuth() {
	c.authedDials.Store(0)
}

func (c *client) ensureAuthForCurrentConn() error {
	if c.isAuthForCurrentConn() {
		// already authenticated
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	return c.ensureAuthForCurrentConnLocked()
}

func (c *client) ensureAuthForCurrentConnLocked() error {
	if c.isAuthForCurrentConn() {
		return nil
	}

	req := contract.AcquireRequest()
	resp := contract.AcquireResponse()
	defer func() {
		contract.ReleaseRequest(req)
		contract.ReleaseResponse(resp)
	}()

	req.SetName(contract.Auth)
	req.Append(c.JwtToken)

	err := c.c.DoDeadline(req, resp, time.Now().Add(c.maxRequestDuration))
	if err != nil {
		return fmt.Errorf("auth is failed: %v", err)
	}

	if resp.GetStatusCode() == base.RPCServerResponseCode_UNAUTHORIZED {
		return ErrorUnauthorized
	}

	if resp.GetStatusCode() != base.RPCServerResponseCode_OK {
		return fmt.Errorf("auth is failed, err = response status code is not RPCServerResponseCode_OK, got = %s", resp.GetStatusCode().String())
	}

	if len(resp.Value()) != 18 {
		return fmt.Errorf("auth is failed, err = response value length is not equal to uuid size + 2 bytes for server id, got = %d", len(resp.Value()))
	}

	buf := resp.Value()

	uid, err := uuid.FromBytes(buf[2:])
	if err != nil {
		return fmt.Errorf("auth is failed, err = parse uid from response is failed: %v", err)
	}

	c.authId = uid
	c.serverID = binary.LittleEndian.Uint16(buf[:2])

	// Bind the Auth to the connection it was performed on. If that connection is
	// already gone, stay unauthenticated: marking the client authenticated here
	// would attribute this Auth to a future connection that never ran it.
	dials := c.connDials.Load()
	if dials > c.connCloses.Load() {
		c.authedDials.Store(dials)
	}

	return nil
}

func (c *client) ensureAuthForCurrentConnDeadline() error {
	if c.isAuthForCurrentConn() {
		return nil
	}

	if !c.mu.TryLock() {
		return fastrpc.ErrTimeout
	}

	errCh := make(chan error, 1)
	go func() {
		defer c.mu.Unlock()
		errCh <- c.ensureAuthForCurrentConnLocked()
	}()

	timer := time.NewTimer(c.maxRequestDuration)
	defer timer.Stop()

	select {
	case err := <-errCh:
		return err
	case <-timer.C:
		return fastrpc.ErrTimeout
	}
}

func (c *client) isAuthForCurrentConn() bool {
	dials := c.connDials.Load()
	return dials > c.connCloses.Load() && dials == c.authedDials.Load()
}

// GetServerID returns the identifier of the server currently associated with the client.
func (c *client) GetServerID() uint16 {
	return c.serverID
}

// GetUUID returns the UUID associated with the current authenticated client connection.
func (c *client) GetUUID() uuid.UUID {
	return c.authId
}

// Reconnects returns the number of connections the client has established.
func (c *client) Reconnects() uint64 {
	return c.connDials.Load()
}

// doUnary sends a unary RPC request with a given proto message and request identifier.
//
// Timing model:
//   - If the underlying fastrpc connection is already open and authenticated for
//     the current connection generation, the call goes straight to
//     fastrpc.Client.DoDeadline with deadline = now + maxRequestDuration.
//     fastrpc does not enforce this deadline with a per-request timer; it relies
//     on an internal stale-request checker. Because of that, the observed call
//     duration may exceed maxRequestDuration by up to the checker wake-up delay
//     (currently up to about 1s in fastrpc).
//   - If the connection is new or was reconnected, doUnary must authenticate the
//     connection first. That path may include TCP dial and the fastrpc protocol
//     handshake (fastrpc has its own 3s handshake deadline). To prevent callers
//     from waiting for that slow path, doUnary wraps authentication in its own
//     timer and returns after maxRequestDuration with NETWORK_ERROR/timeout if
//     auth doesn't finish in time.
//   - Only one goroutine is allowed to run the auth/reconnect path for a client.
//     Concurrent callers that arrive while auth is already in progress fail fast
//     with NETWORK_ERROR/timeout instead of each waiting for maxRequestDuration.
//   - After successful authentication, the main RPC request is still handled by
//     fastrpc.DoDeadline as described in the first case, so the strict SDK-level
//     timeout currently applies to the auth/reconnect path only, not to the main
//     request on an already authenticated connection.
func (c *client) doUnary(req, resp proto.Message, reqn contract.RPCRegister) (proto.Message, base.RPCServerResponseCode, error) {
	raw, err := proto.Marshal(req)
	if nil != err {
		return nil, base.RPCServerResponseCode_UNKNOWN, fmt.Errorf("marshal request %s is failed: %+v", reqn, err)
	}

	if 0 == len(raw) {
		return nil, base.RPCServerResponseCode_UNKNOWN, fmt.Errorf("marshal request %s is failed: empty request", reqn)
	}

	// The connection may still be replaced under us between the auth check and
	// the actual write: fastrpc re-dials transparently and flushes whatever is
	// already queued onto the new, unauthenticated connection. The server checks
	// the payer identity before it does any work, so an UNAUTHORIZED response has
	// no side effects and the request can be re-authenticated and sent once more.
	for attempt := 0; ; attempt++ {
		if !c.disableAuth {
			if err = c.ensureAuthForCurrentConnDeadline(); err != nil {
				if errors.Is(err, fastrpc.ErrTimeout) {
					return nil, base.RPCServerResponseCode_NETWORK_ERROR, err
				}
				return nil, base.RPCServerResponseCode_UNAUTHORIZED, err
			}
		}

		result, statusCode, err := c.send(raw, resp, reqn)
		if statusCode == base.RPCServerResponseCode_UNAUTHORIZED && attempt == 0 && !c.disableAuth {
			c.metricGroups[reqn].authRetry.Inc()
			c.invalidateAuth()
			continue
		}

		return result, statusCode, err
	}
}

// send writes an already marshalled request to the current connection and
// decodes the response into resp.
func (c *client) send(raw []byte, resp proto.Message, reqn contract.RPCRegister) (proto.Message, base.RPCServerResponseCode, error) {
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
	err := c.c.DoDeadline(rpcReq, rpcResp, st.Add(c.maxRequestDuration))
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
