package client

import (
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/internal/contract"
	"gitlab.adtelligent.com/common/shared/log"
	"google.golang.org/protobuf/proto"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type ShardedClient struct {
	Addrs              string
	MaxRequestDuration time.Duration

	roundRobin uint64

	once    sync.Once
	clients []*client
}

func (sc *ShardedClient) Mock(req *base.MockRequest, resp proto.Message) (proto.Message, base.RPCServerResponseCode, error) {
	res, statusCode, err := sc.getClient().doUnary(req, resp, contract.Mock)
	if nil != err {
		return nil, statusCode, err
	}
	return res, statusCode, nil
}

func (sc *ShardedClient) Target(req *base.TargetRequest) (*base.TargetResponse, base.RPCServerResponseCode, error) {
	res, statusCode, err := sc.getClient().doUnary(req, &base.TargetResponse{}, contract.Target)
	if nil != err {
		return nil, statusCode, err
	}
	return res.(*base.TargetResponse), statusCode, nil
}

func (sc *ShardedClient) Report(req *base.ReportRequest) (base.RPCServerResponseCode, error) {
	_, statusCode, err := sc.getClient().doUnary(req, nil, contract.Report)
	return statusCode, err
}

func (sc *ShardedClient) getClient() *client {
	sc.once.Do(sc.initClients)

	n := atomic.AddUint64(&sc.roundRobin, 1)
	idx := n % uint64(len(sc.clients))

	return sc.clients[idx]
}

func (sc *ShardedClient) initClients() {
	if len(sc.Addrs) == 0 {
		log.Panicf("BUG: ShardedClient.Addrs cannot be empty")
	}
	for _, addr := range strings.Split(sc.Addrs, ",") {
		c := newClient(addr, sc.MaxRequestDuration)
		sc.clients = append(sc.clients, c)
	}
}

func (sc *ShardedClient) PendingRequests() int {
	n := 0
	for _, c := range sc.clients {
		n += c.c.PendingRequests()
	}
	return n
}
