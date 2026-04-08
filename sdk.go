package dcr_sdk

import (
	"flag"
	"github.com/VictoriaMetrics/metrics"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/internal/client"
	"google.golang.org/protobuf/proto"
	"sync"
	"time"
)

var (
	sdkRPCMaxRequestDuration = flag.Duration("dcrMaxRequestDuration", time.Second, "Maximum duration for each DCR RPC request")
	sdkRPCServers            = flag.String("dcrServers", "127.0.0.1:7943", "Comma-separated list of DCR servers")
)

func Target(req *base.TargetRequest) (res *base.TargetResponse, err error) {
	return getClient().Target(req)
}

func Report(req *base.ReportRequest) error {
	return getClient().Report(req)
}

func Mock(req *base.MockRequest, resp proto.Message) (msg proto.Message, err error) {
	return getClient().Mock(req, resp)
}

var (
	shardedClient         *client.ShardedClient
	initShardedClientOnce sync.Once
)

func getClient() *client.ShardedClient {
	initShardedClientOnce.Do(initShardedClient)
	return shardedClient
}

func initShardedClient() {
	if !flag.Parsed() {
		panic("BUG: flag.Parse() must be called before calling sdkRPC methods")
	}
	if len(*sdkRPCServers) == 0 {
		panic("BUG: sdkRPCServers cannot be empty")
	}
	shardedClient = &client.ShardedClient{
		Addrs:              *sdkRPCServers,
		MaxRequestDuration: *sdkRPCMaxRequestDuration,
	}

	metrics.NewGauge("sdkRPCClientPendingRequests", func() float64 {
		return float64(shardedClient.PendingRequests())
	})
}
