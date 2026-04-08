package client

import (
	"errors"
	"fmt"
	"github.com/aradilov/fastrpc"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/internal/contract"
	"google.golang.org/protobuf/proto"
	"time"
)

type client struct {

	// maxRequestDuration specifies the maximum duration allowed for a single request to complete before timing out.
	maxRequestDuration time.Duration

	// metricGroups is an array of metricsGroup pointers, indexed by request identifiers, for tracking metrics of RPC calls.
	metricGroups [contract.MaxRequestIdentifier + 1]*metricsGroup

	c *fastrpc.Client
}

// doUnary sends a unary gRPC request with a given proto message and request identifier, and returns the response or an error.
func (c *client) doUnary(req, resp proto.Message, reqn contract.RPCRegister) (proto.Message, base.RPCServerResponseCode, error) {
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
