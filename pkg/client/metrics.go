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
		error:    metrics.GetOrCreateCounter(fmt.Sprintf(`dcrRPCClientError{request=%q,addr=%q,err="other"}`, request, addr)),
		failed:   metrics.GetOrCreateCounter(fmt.Sprintf(`dcrRPCClientError{request=%q,addr=%q,err="failed"}`, request, addr)),
		timeout:  metrics.GetOrCreateCounter(fmt.Sprintf(`dcrRPCClientError{request=%q,addr=%q,err="timeout"}`, request, addr)),
		overflow: metrics.GetOrCreateCounter(fmt.Sprintf(`dcrRPCClientError{request=%q,addr=%q,err="overflow"}`, request, addr)),
		success:  metrics.GetOrCreateCounter(fmt.Sprintf(`dcrRPCClientSuccess{request=%q,addr=%q}`, request, addr)),
		request:  metrics.GetOrCreateCounter(fmt.Sprintf(`dcrRPCClientRequest{request=%q,addr=%q}`, request, addr)),
		duration: metrics.GetOrCreateHistogram(fmt.Sprintf(`dcrRPCClientDuration{request=%q,addr=%q}`, request, addr)),
	}
}
