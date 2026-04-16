package client

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aradilov/fastrpc"
	"github.com/google/uuid"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/internal/contract"
	"google.golang.org/protobuf/proto"
)

var ErrorUnauthorized = errors.New("unauthorized")

type client struct {

	// JvtToken is a byte slice used for storing JSON Web Token (JWT) data associated with the client.
	JvtToken []byte

	// maxRequestDuration specifies the maximum duration allowed for a single request to complete before timing out.
	maxRequestDuration time.Duration

	// metricGroups is an array of metricsGroup pointers, indexed by request identifiers, for tracking metrics of RPC calls.
	metricGroups [contract.MaxRequestIdentifier + 1]*metricsGroup

	// single connection RPC client
	c *fastrpc.Client

	connGen atomic.Uint64
	mu      sync.Mutex

	authedGen uint64
	authErr   error
	authId    uuid.UUID
}

func (c *client) ensureAuthForCurrentConn() error {
	gen := c.connGen.Load()
	if gen > 0 && gen == atomic.LoadUint64(&c.authedGen) {
		// already authenticated
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	gen = c.connGen.Load()
	if gen > 0 && gen == c.authedGen {
		return nil
	}

	req := contract.AcquireRequest()
	resp := contract.AcquireResponse()
	defer func() {
		contract.ReleaseRequest(req)
		contract.ReleaseResponse(resp)
	}()

	req.SetName(contract.Auth)
	req.Append(c.JvtToken)

	err := c.c.DoDeadline(req, resp, time.Now().Add(time.Second))
	if err != nil {
		c.authErr = fmt.Errorf("auth is failed: %v", err)
		return c.authErr
	}

	if resp.GetStatusCode() == base.RPCServerResponseCode_UNAUTHORIZED {
		c.authErr = ErrorUnauthorized
		return c.authErr
	}

	if resp.GetStatusCode() != base.RPCServerResponseCode_OK {
		c.authErr = fmt.Errorf("auth is failed, err = response status code is not RPCServerResponseCode_OK, got = %s", resp.GetStatusCode().String())
		return c.authErr
	}

	uid, err := uuid.Parse(string(resp.Value()))
	if err != nil {
		c.authErr = fmt.Errorf("auth is failed, err = parse uid from response is failed: %v", err)
		return c.authErr
	}

	c.authedGen = gen
	c.authErr = nil
	c.authId = uid

	return nil
}

// GetUUID returns the UUID associated with the current authenticated client connection.
func (c *client) GetUUID() uuid.UUID {
	return c.authId
}

// Reconnects returns the number of reconnections the client has performed by reading the connection generation counter.
func (c *client) Reconnects() uint64 {
	return c.connGen.Load()
}

// doUnary sends a unary gRPC request with a given proto message and request identifier, and returns the response or an error.
func (c *client) doUnary(req, resp proto.Message, reqn contract.RPCRegister) (proto.Message, base.RPCServerResponseCode, error) {
	if err := c.ensureAuthForCurrentConn(); err != nil {
		return nil, base.RPCServerResponseCode_UNAUTHORIZED, err
	}

	raw, err := proto.Marshal(req)
	if 0 == len(raw) {
		return nil, base.RPCServerResponseCode_UNKNOWN, fmt.Errorf("marshal request %s is failed: empty request", reqn)
	}

	if nil != err {
		return nil, base.RPCServerResponseCode_UNKNOWN, fmt.Errorf("marshal request %s is failed: %+v", reqn, err)
	}

	metricGroup := c.metricGroups[reqn]

	st := time.Now()
	rpcReq := contract.AcquireRequest()
	rpcResp := contract.AcquireResponse()

	rpcReq.SetName(reqn)
	rpcReq.Append(raw)

	err = c.c.DoDeadline(rpcReq, rpcResp, st.Add(c.maxRequestDuration))
	metricGroup.duration.UpdateDuration(st)
	contract.ReleaseRequest(rpcReq)
	if err != nil {
		c.countError(reqn, err, rpcResp)
		contract.ReleaseResponse(rpcResp)
		return nil, base.RPCServerResponseCode_NETWORK_ERROR, fmt.Errorf("error when calling '%s': %s", reqn, err)
	}

	statusCode := rpcResp.GetStatusCode()
	if statusCode != base.RPCServerResponseCode_OK {
		err = fmt.Errorf("RPC[%q]: %s", statusCode.String(), rpcResp.Value())
		c.countError(reqn, nil, rpcResp)
		contract.ReleaseResponse(rpcResp)
		return nil, statusCode, err
	}

	if nil != resp {
		err = proto.Unmarshal(rpcResp.Value(), resp)
		contract.ReleaseResponse(rpcResp)
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
