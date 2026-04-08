package client

import (
	"fmt"
	"github.com/aradilov/fastrpc"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/internal/contract"
	"github.com/mygaru/dcr-sdk/internal/sdkutil"
	"github.com/valyala/fasthttp"
	"google.golang.org/protobuf/proto"
	"net"
	"time"
)

type client struct {
	maxRequestDuration time.Duration

	metricGroups [contract.MaxRequestIdentifier]*metricsGroup

	c *fastrpc.Client
}

func newClient(addr string, maxRequestDuration time.Duration) *client {

	if maxRequestDuration <= 0 {
		maxRequestDuration = time.Second
	}

	c := &client{
		maxRequestDuration: maxRequestDuration,

		c: &fastrpc.Client{
			SniffHeader:     sdkutil.SniffHeader,
			ProtocolVersion: sdkutil.ProtocolVersion,

			NewResponse: func() fastrpc.ResponseReader {
				return &contract.Response{}
			},

			Dial: func(addr string) (conn net.Conn, err error) {
				return fasthttp.DialTimeout(addr, maxRequestDuration)
			},

			Addr: addr,

			// read timeout should be quite high in order to avoid
			// frequent reconnects for almost idle connections.
			ReadTimeout: time.Minute,

			WriteTimeout:       maxRequestDuration * 10,
			MaxPendingRequests: 4e3,

			CompressType: fastrpc.CompressSnappy,

			WriteBufferSize: 4 * 1024,
			ReadBufferSize:  4 * 1024,
		},
	}

	for i := 1; i < int(contract.MaxRequestIdentifier); i++ {
		c.metricGroups[i] = newMetricsGroup(addr, contract.RPCRegister(i).String())
	}
	return c
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

	switch err {
	case fastrpc.ErrTimeout:
		metricGroup.timeout.Inc()
	case fastrpc.ErrPendingRequestsOverflow:
		metricGroup.overflow.Inc()
	default:
		metricGroup.error.Inc()
	}
}
