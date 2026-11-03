package dcr_sdk

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aradilov/fastrpc"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/internal/testcloud"
	"github.com/mygaru/dcr-sdk/pkg/client"
)

func newTestClientWith(t *testing.T, cfg client.Configuration) *client.ShardedClient {
	t.Helper()

	rpc, err := NewWithMTLS(&cfg, testMTLSConfig())
	if err != nil {
		t.Fatalf("create test client: %v", err)
	}
	return rpc
}

func testReportRequest() *base.ReportRequest {
	return &base.ReportRequest{
		Payer:      testPayer.String(),
		TrackingId: []byte("0400000000000001"),
		Event:      base.EventType_EVENT_TYPE_BILLING,
		Rules:      []*base.ReportRequest_Rule{{EventsCount: 1, SegmentIds: []uint32{1}}},
	}
}

// TestTargetOverflowDoesNotReachReport is the reason Report has connections of
// its own: Targets filling every in-flight slot of their pool used to fail the
// reports sent next to them with "Pending requests overflow".
func TestTargetOverflowDoesNotReachReport(t *testing.T) {
	server := startTestCloud(t, testcloud.Config{ServerID: 1024, TargetDelay: 300 * time.Millisecond})
	rpc := newTestClientWith(t, client.Configuration{
		Addrs:                          server.Addr(),
		MaximumSimultaneousConnections: 1,
		MaxPendingRequests:             2,
	})

	var wg sync.WaitGroup
	defer wg.Wait()
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = rpc.Target(testTargetRequest())
		}()
	}
	waitFor(t, func() bool { return rpc.PendingRequests() > 0 })

	if _, _, err := rpc.Target(testTargetRequest()); !errors.Is(err, fastrpc.ErrPendingRequestsOverflow) {
		t.Fatalf("expected the Target pool to overflow, got %v", err)
	}

	started := time.Now()
	if sc, err := rpc.Report(testReportRequest()); err != nil {
		t.Fatalf("report failed with %s: %v", sc, err)
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("report waited %s behind the Targets", elapsed)
	}
}

// TestReportWaitsForAFreeSlot covers the overflow of the Report pool itself: a
// report that finds every slot taken waits for one instead of failing.
func TestReportWaitsForAFreeSlot(t *testing.T) {
	server := startTestCloud(t, testcloud.Config{ServerID: 1024, ReportDelay: 50 * time.Millisecond, ReportBuffer: 16})
	rpc := newTestClientWith(t, client.Configuration{
		Addrs:                    server.Addr(),
		ReportConnections:        1,
		ReportMaxPendingRequests: 2,
		ReportRetries:            -1,
	})

	const reports = 5
	errs := make(chan error, reports)
	var wg sync.WaitGroup
	for range reports {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if sc, err := rpc.Report(testReportRequest()); err != nil {
				errs <- fmt.Errorf("%s: %w", sc, err)
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("report failed: %v", err)
	}
	if got := server.ReportAttempts(); got != reports {
		t.Fatalf("expected %d reports to reach the cloud, got %d", reports, got)
	}
}

func TestReportRetriesTransientFailures(t *testing.T) {
	server := startTestCloud(t, testcloud.Config{ServerID: 1024, ReportFailures: 2})
	rpc := newTestClientWith(t, client.Configuration{Addrs: server.Addr(), ReportRetryBackoff: time.Millisecond})

	if sc, err := rpc.Report(testReportRequest()); err != nil {
		t.Fatalf("report failed with %s: %v", sc, err)
	}
	if got := server.ReportAttempts(); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestReportGivesUpAfterReportRetries(t *testing.T) {
	server := startTestCloud(t, testcloud.Config{ServerID: 1024, ReportFailures: 100})
	rpc := newTestClientWith(t, client.Configuration{
		Addrs:              server.Addr(),
		ReportRetries:      2,
		ReportRetryBackoff: time.Millisecond,
	})

	sc, err := rpc.Report(testReportRequest())
	if err == nil || sc != base.RPCServerResponseCode_SERVICE_UNAVAILABLE {
		t.Fatalf("expected SERVICE_UNAVAILABLE, got %s: %v", sc, err)
	}
	if got := server.ReportAttempts(); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestReportRetriesCanBeDisabled(t *testing.T) {
	server := startTestCloud(t, testcloud.Config{ServerID: 1024, ReportFailures: 1})
	rpc := newTestClientWith(t, client.Configuration{Addrs: server.Addr(), ReportRetries: -1})

	if _, err := rpc.Report(testReportRequest()); err == nil {
		t.Fatalf("expected the report to fail")
	}
	if got := server.ReportAttempts(); got != 1 {
		t.Fatalf("expected 1 attempt, got %d", got)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("condition not met in time")
		}
		time.Sleep(time.Millisecond)
	}
}
