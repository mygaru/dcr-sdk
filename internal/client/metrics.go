package client

import (
	"fmt"
	"github.com/VictoriaMetrics/metrics"
)

type metricsGroup struct {
	error    *metrics.Counter
	success  *metrics.Counter
	timeout  *metrics.Counter
	failed   *metrics.Counter
	overflow *metrics.Counter
	request  *metrics.Counter
	duration *metrics.Histogram
}

func newMetricsGroup(request, addr string) *metricsGroup {
	return &metricsGroup{
		error:    metrics.NewCounter(fmt.Sprintf(`dcrRPCClientError{request=%q,addr=%q,err="other"}`, request, addr)),
		failed:   metrics.NewCounter(fmt.Sprintf(`dcrRPCClientError{request=%q,addr=%q,err="failed"}`, request, addr)),
		timeout:  metrics.NewCounter(fmt.Sprintf(`dcrRPCClientError{request=%q,addr=%q,err="timeout"}`, request, addr)),
		overflow: metrics.NewCounter(fmt.Sprintf(`dcrRPCClientError{request=%q,addr=%q,err="overflow"}`, request, addr)),
		success:  metrics.NewCounter(fmt.Sprintf(`dcrRPCClientSuccess{request=%q,addr=%q}`, request, addr)),
		request:  metrics.NewCounter(fmt.Sprintf(`dcrRPCClientRequest{request=%q,addr=%q}`, request, addr)),
		duration: metrics.NewHistogram(fmt.Sprintf(`dcrRPCClientDuration{request=%q,addr=%q}`, request, addr)),
	}
}
