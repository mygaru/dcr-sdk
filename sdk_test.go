package dcr_sdk

import (
	"crypto/x509"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"gitlab.adtelligent.com/awesome/mtls"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/internal/testcloud"

	"github.com/mygaru/dcr-sdk/pkg/client"
)

const MaximumSimultaneousConnections = 4

// testPayer is the caller identity the tests put on their requests. Requests
// carry it themselves now, so there is no per-connection identity to set up.
var testPayer = uuid.New()

func getTestClient(t *testing.T) *client.ShardedClient {
	t.Helper()

	server := startTestCloud(t, testcloud.Config{})
	return newTestClient(server.Addr(), MaximumSimultaneousConnections)
}

// testCerts is the PKI every test client and test cloud share: the cloud
// serves mTLS only, so there is no test without certificates.
var testCerts = func() *testcloud.Certificates {
	certs, err := testcloud.NewCertificates()
	if err != nil {
		panic(fmt.Sprintf("generate test certificates: %v", err))
	}
	return certs
}()

func testMTLSConfig() MTLSConfig {
	return MTLSConfig{
		CertPEM:         testCerts.ClientCertPEM,
		KeyPEM:          testCerts.ClientKeyPEM,
		ServerRootCAs:   testCerts.ServerRoots,
		ServerName:      testCerts.ServerName,
		ClientCertRoots: testCerts.ClientRoots,
	}
}

func newTestClient(addr string, maximumSimultaneousConnections int) *client.ShardedClient {
	rpc, err := NewWithMTLS(&client.Configuration{
		Addrs:                          addr,
		MaximumSimultaneousConnections: maximumSimultaneousConnections,
	}, testMTLSConfig())
	if err != nil {
		panic(fmt.Sprintf("create test client: %v", err))
	}
	return rpc
}

func TestTargetReturnsTrackingIDAndReportUsesIt(t *testing.T) {
	server := startTestCloud(t, testcloud.Config{ServerID: 1024})
	rpc := newTestClient(server.Addr(), 2)

	resp, sc, err := rpc.Target(&base.TargetRequest{
		Uids: []*base.UID{
			{Id: []byte(uuid.New().String()), Type: base.UID_DEVICE_ID},
		},
		Match: []*base.Match_Rule{
			{TrafficType: base.TrafficType_TRAFFIC_TYPE_VIDEO, SegmentIds: []uint32{1, 2, 3}, Payer: testPayer.String()},
		},
	})
	if nil != err {
		t.Fatalf("expected error to be %v, got %v", nil, err)
	}
	if sc != base.RPCServerResponseCode_OK {
		t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_OK, sc)
	}
	if len(resp.TrackingId) != 16 {
		t.Fatalf("expected 16-byte tracking id, got %q", resp.TrackingId)
	}
	if !rpc.IsValidTrackingID(resp.TrackingId) {
		t.Fatalf("expected tracking id %q to be valid for this client", resp.TrackingId)
	}

	report := &base.ReportRequest{
		Payer:      testPayer.String(),
		TrackingId: resp.TrackingId,
		Event:      base.EventType_EVENT_TYPE_CLICK,
		Rules: []*base.ReportRequest_Rule{
			{
				TrafficType: base.TrafficType_TRAFFIC_TYPE_VIDEO,
				EventsCount: 1,
				SegmentIds:  []uint32{1, 2, 3},
			},
		},
	}
	sc, err = rpc.Report(report)
	if nil != err {
		t.Fatalf("expected nil, got err %v", err)
	}
	if sc != base.RPCServerResponseCode_OK {
		t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_OK, sc)
	}

	got := nextReport(t, server)
	if string(got.TrackingId) != string(report.TrackingId) {
		t.Fatalf("expected report tracking id %q, got %q", report.TrackingId, got.TrackingId)
	}
	if got.Event != report.Event {
		t.Fatalf("expected report event %s, got %s", report.Event, got.Event)
	}
}

func TestTargetUsesEveryConnectionInThePool(t *testing.T) {
	rpc := getTestClient(t)
	for i := 0; i < 100; i++ {
		_, sc, err := rpc.Target(testTargetRequest())
		if nil != err {
			t.Fatalf("expected nil, got err %v", err)
		}

		if sc != base.RPCServerResponseCode_OK {
			t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_OK, sc)
		}
	}

	if rpc.Reconnects() != MaximumSimultaneousConnections {
		t.Fatalf("expected %d reconnects, got %d", MaximumSimultaneousConnections, rpc.Reconnects())
	}
}

func TestCallsRejectMissingPayer(t *testing.T) {
	// No payer on the request: the SDK must refuse locally
	// rather than send a request the cloud will reject.
	server := startTestCloud(t, testcloud.Config{ServerID: 1024})
	rpc := newTestClient(server.Addr(), 1)

	req := testTargetRequest()
	req.Match[0].Payer = ""

	_, sc, err := rpc.Target(req)
	if !errors.Is(err, client.ErrorPayerRequired) {
		t.Fatalf("Target: expected ErrorPayerRequired, got %v", err)
	}
	if sc != base.RPCServerResponseCode_INVALID_REQUEST {
		t.Fatalf("Target: expected INVALID_REQUEST, got %s", sc)
	}

	sc, err = rpc.Report(&base.ReportRequest{
		TrackingId: []byte("0400000000000001"),
		Rules:      []*base.ReportRequest_Rule{{EventsCount: 1}},
	})
	if !errors.Is(err, client.ErrorPayerRequired) {
		t.Fatalf("Report: expected ErrorPayerRequired, got %v", err)
	}
	if sc != base.RPCServerResponseCode_INVALID_REQUEST {
		t.Fatalf("Report: expected INVALID_REQUEST, got %s", sc)
	}
}

func TestTargetPutsPayerOnTheRequest(t *testing.T) {
	server := startTestCloud(t, testcloud.Config{ServerID: 1024})
	rpc := newTestClient(server.Addr(), 1)

	req := testTargetRequest()
	if _, _, err := rpc.Target(req); err != nil {
		t.Fatalf("Target: %v", err)
	}
	if got := req.GetMatch()[0].GetPayer(); got != testPayer.String() {
		t.Fatalf("expected the SDK to keep payer %s on the match rule, got %q", testPayer, got)
	}
	if got := server.Unauthorized(); got != 0 {
		t.Fatalf("expected no request without a payer to reach the server, got %d", got)
	}
}

func TestNewWithMTLSUsesClientCertificate(t *testing.T) {
	server := startTestCloud(t, testcloud.Config{})
	rpc, err := NewWithMTLS(&client.Configuration{
		Addrs:                          server.Addr(),
		MaximumSimultaneousConnections: 1,
	}, testMTLSConfig())
	if err != nil {
		t.Fatalf("create mTLS client: %v", err)
	}

	_, sc, err := rpc.Target(testTargetRequest())
	if err != nil {
		t.Fatalf("expected nil, got err %v", err)
	}
	if sc != base.RPCServerResponseCode_OK {
		t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_OK, sc)
	}
	// the test cloud refuses any connection that did not present a certificate
	if got := server.Unauthorized(); got != 0 {
		t.Fatalf("expected the connection to be authenticated by its certificate, got %d refused", got)
	}
}

func TestNewWithMTLSRejectsMissingCertificate(t *testing.T) {
	if _, err := NewWithMTLS(nil, MTLSConfig{}); err == nil {
		t.Fatal("expected an error without a client certificate")
	}
}

func TestNewWithMTLSRejectsInvalidClientCertificate(t *testing.T) {
	cert, err := mtls.GenerateSelfSigned(mtls.GenerateConfig{
		CN:   "dcr-sdk-client",
		UUID: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("generate client certificate: %v", err)
	}

	wrongRoots := x509.NewCertPool()
	ca, err := mtls.GenerateCA(mtls.GenerateCAConfig{CN: "untrusted-ca"})
	if err != nil {
		t.Fatalf("generate CA: %v", err)
	}
	wrongRoots.AddCert(ca.Cert)

	_, err = NewWithMTLS(&client.Configuration{}, MTLSConfig{
		CertPEM:         cert.CertPEM,
		KeyPEM:          cert.KeyPEM,
		ClientCertRoots: wrongRoots,
	})
	if err == nil {
		t.Fatalf("expected invalid client certificate error")
	}
}

func TestTargetReturnsServerStatusError(t *testing.T) {
	server := startTestCloud(t, testcloud.Config{
		TargetStatus: base.RPCServerResponseCode_SERVICE_UNAVAILABLE,
	})
	rpc := newTestClient(server.Addr(), 1)

	resp, sc, err := rpc.Target(&base.TargetRequest{
		Uids: []*base.UID{
			{Id: []byte(uuid.New().String()), Type: base.UID_DEVICE_ID},
		},
	})
	if nil == err {
		t.Fatalf("expected target error")
	}
	if resp != nil {
		t.Fatalf("expected nil target response, got %+v", resp)
	}
	if sc != base.RPCServerResponseCode_SERVICE_UNAVAILABLE {
		t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_SERVICE_UNAVAILABLE, sc)
	}
}

func TestReportRejectsMissingTrackingID(t *testing.T) {
	rpc := getTestClient(t)

	sc, err := rpc.Report(&base.ReportRequest{Payer: testPayer.String()})
	if nil == err {
		t.Fatalf("expected missing tracking id error")
	}
	if sc != base.RPCServerResponseCode_UNKNOWN {
		t.Fatalf("expected status code to be %d, got %d", base.RPCServerResponseCode_UNKNOWN, sc)
	}
}

// TestReportSendsUnknownOriginNodeAnyway covers the routing that was removed: a
// tracking id from a node this client holds no connection to used to fail
// locally with "unknown server for tracking id" and never leave the process.
// The cloud forwards such a report to its origin node, so the client must send
// it and let the cloud answer.
func TestReportSendsUnknownOriginNodeAnyway(t *testing.T) {
	server := startTestCloud(t, testcloud.Config{ServerID: 1024})
	rpc := newTestClient(server.Addr(), 1)

	// FFFF is not this server's id, and the client never talked to that node.
	if _, err := rpc.Report(&base.ReportRequest{
		Payer:      testPayer.String(),
		TrackingId: []byte("FFFF000000000001"),
		Event:      base.EventType_EVENT_TYPE_IMPRESSION,
		Rules:      []*base.ReportRequest_Rule{{EventsCount: 1}},
	}); err != nil {
		t.Fatalf("expected the report to be sent, got %v", err)
	}

	if got := nextReport(t, server); string(got.GetTrackingId()) != "FFFF000000000001" {
		t.Fatalf("report tracking id = %q, want it to reach the server", got.GetTrackingId())
	}
}

// TestIsValidTrackingIDChecksFormatOnly pins the narrowed contract: it answers
// "is this well formed", not "can this client route it".
func TestIsValidTrackingIDChecksFormatOnly(t *testing.T) {
	rpc := newTestClient("127.0.0.1:1", 1)

	if !rpc.IsValidTrackingID([]byte("FFFF000000000001")) {
		t.Error("a well-formed tracking id from an unknown node must be valid")
	}
	if rpc.IsValidTrackingID(nil) {
		t.Error("nil must not be valid")
	}
	if rpc.IsValidTrackingID([]byte("short")) {
		t.Error("a truncated tracking id must not be valid")
	}
	if rpc.IsValidTrackingID([]byte("ZZZZ000000000001")) {
		t.Error("a non-hex origin id must not be valid")
	}
}

func TestNewWithMTLSDefaults(t *testing.T) {
	tests := []struct {
		name       string
		cfg        *client.Configuration
		expectAddr string
	}{
		{name: "nil configuration", cfg: nil, expectAddr: "cloud.mygaru.com:7937"},
		{name: "empty configuration", cfg: &client.Configuration{}, expectAddr: "cloud.mygaru.com:7937"},
		{name: "addr configuration", cfg: &client.Configuration{Addrs: "anyaddr.here:8080"}, expectAddr: "anyaddr.here:8080"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sdk, err := NewWithMTLS(tt.cfg, testMTLSConfig())
			if err != nil {
				t.Fatalf("NewWithMTLS: %v", err)
			}
			if sdk.Configuration.Addrs != tt.expectAddr {
				t.Errorf("expected addrs to be %q, got %q", tt.expectAddr, sdk.Configuration.Addrs)
			}
		})
	}
}

func testTargetRequest() *base.TargetRequest {
	return &base.TargetRequest{
		Uids: []*base.UID{
			{Id: []byte(uuid.New().String()), Type: base.UID_DEVICE_ID},
		},
		Match: []*base.Match_Rule{
			{TrafficType: base.TrafficType_TRAFFIC_TYPE_VIDEO, SegmentIds: []uint32{1, 2, 3}, Payer: testPayer.String()},
		},
	}
}

func startTestCloud(t *testing.T, cfg testcloud.Config) *testcloud.Server {
	t.Helper()

	if cfg.TLSConfig == nil {
		cfg.TLSConfig = testCerts.ServerTLSConfig
	}
	server, err := testcloud.Start(cfg)
	if err != nil {
		t.Fatalf("start test-cloud: %v", err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("stop test-cloud: %v", err)
		}
	})
	return server
}

func nextReport(t *testing.T, server *testcloud.Server) *base.ReportRequest {
	t.Helper()

	select {
	case report := <-server.Reports():
		return report
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for report request")
		return nil
	}
}

// TestTargetSurvivesConnectionChurn guards the reason the payer moved into the
// request. While the payer lived on the TCP connection, the first Target/Report
// after every disconnect was written to a fresh, unauthenticated connection and
// rejected with UNAUTHORIZED "payer identity is missing". A self-describing
// request cannot lose its identity to a reconnect, so no request may arrive
// without a payer no matter how often the connection is dropped.
func TestTargetSurvivesConnectionChurn(t *testing.T) {
	server := startTestCloud(t, testcloud.Config{ServerID: 1024, DropConnAfterRequest: true})
	rpc := newTestClient(server.Addr(), 1)

	const requests = 12
	for i := 0; i < requests; i++ {
		_, statusCode, err := rpc.Target(testTargetRequest())
		if err != nil {
			t.Fatalf("request %d failed with %s: %v", i, statusCode, err)
		}
		// Let the server-side drop land before the next request.
		time.Sleep(60 * time.Millisecond)
	}

	if got := server.Unauthorized(); got != 0 {
		t.Fatalf("expected no unauthenticated requests to reach the server, got %d", got)
	}
	if got := rpc.Reconnects(); got < requests {
		t.Fatalf("expected at least %d reconnects to be exercised, got %d", requests, got)
	}
}

// TestReportBillsSeveralClientsInOneCall is the reason the payer moved onto the
// rule: a platform reporting for its own clients names each of them per rule and
// sends one report.
func TestReportBillsSeveralClientsInOneCall(t *testing.T) {
	clientA := uuid.New()
	clientB := uuid.New()
	platform := uuid.New()

	server := startTestCloud(t, testcloud.Config{ServerID: 1024})
	rpc := newTestClient(server.Addr(), 1)

	targetReq := testTargetRequest()
	targetReq.Match[0].Payer = platform.String()
	targetResp, _, err := rpc.Target(targetReq)
	if err != nil {
		t.Fatalf("target: %v", err)
	}

	req := &base.ReportRequest{
		Payer:      platform.String(),
		TrackingId: targetResp.GetTrackingId(),
		Event:      base.EventType_EVENT_TYPE_IMPRESSION,
		Rules: []*base.ReportRequest_Rule{
			{TrafficType: base.TrafficType_TRAFFIC_TYPE_VIDEO, EventsCount: 1, SegmentIds: []uint32{1}, Payer: clientA.String()},
			{TrafficType: base.TrafficType_TRAFFIC_TYPE_DISPLAY, EventsCount: 2, SegmentIds: []uint32{2}, Payer: clientB.String()},
			// No payer: inherits the request-level payer.
			{TrafficType: base.TrafficType_TRAFFIC_TYPE_VIDEO_SENSITIVE, EventsCount: 3, SegmentIds: []uint32{3}},
		},
	}

	if _, err = rpc.Report(req); err != nil {
		t.Fatalf("report: %v", err)
	}

	want := []string{clientA.String(), clientB.String(), platform.String()}
	for i, rule := range req.GetRules() {
		if rule.GetPayer() != want[i] {
			t.Errorf("rule %d payer = %q, want %q", i, rule.GetPayer(), want[i])
		}
	}

	got := nextReport(t, server)
	for i, rule := range got.GetRules() {
		if rule.GetPayer() != want[i] {
			t.Errorf("rule %d payer on the wire = %q, want %q", i, rule.GetPayer(), want[i])
		}
	}
	if n := server.Unauthorized(); n != 0 {
		t.Fatalf("expected every rule to name a payer, got %d unattributed", n)
	}
}

// TestTargetChecksSegmentsOfSeveralClients is the reason the payer sits on
// Match.Rule: one Target can ask for the segments of several clients at once,
// each rule naming the client whose segment access is evaluated.
func TestTargetChecksSegmentsOfSeveralClients(t *testing.T) {
	clientA := uuid.New()
	clientB := uuid.New()
	platform := uuid.New()

	server := startTestCloud(t, testcloud.Config{ServerID: 1024})
	rpc := newTestClient(server.Addr(), 1)

	req := &base.TargetRequest{
		Uids: []*base.UID{
			{Id: []byte(uuid.New().String()), Type: base.UID_DEVICE_ID},
		},
		Match: []*base.Match_Rule{
			{TrafficType: base.TrafficType_TRAFFIC_TYPE_VIDEO, SegmentIds: []uint32{1}, Payer: clientA.String()},
			{TrafficType: base.TrafficType_TRAFFIC_TYPE_DISPLAY, SegmentIds: []uint32{2}, Payer: clientB.String()},
			{TrafficType: base.TrafficType_TRAFFIC_TYPE_VIDEO_SENSITIVE, SegmentIds: []uint32{3}, Payer: platform.String()},
		},
	}

	if _, sc, err := rpc.Target(req); err != nil {
		t.Fatalf("target: %s: %v", sc, err)
	}

	want := []string{clientA.String(), clientB.String(), platform.String()}
	for i, rule := range req.GetMatch() {
		if rule.GetPayer() != want[i] {
			t.Errorf("match rule %d payer = %q, want %q", i, rule.GetPayer(), want[i])
		}
	}
	if n := server.Unauthorized(); n != 0 {
		t.Fatalf("expected every rule to name a payer, got %d unattributed", n)
	}
}

// TestRulePayerSurvivesTheRequestPayer pins that stamping the request-level
// payer never overwrites a payer a rule already names - otherwise a mixed
// request would silently collapse onto one client.
func TestRulePayerSurvivesTheRequestPayer(t *testing.T) {
	client := uuid.New()
	platform := uuid.New()

	rpc := newTestClient("127.0.0.1:1", 1)

	req := &base.ReportRequest{
		Payer:      platform.String(),
		TrackingId: []byte("0400000000000001"),
		Rules: []*base.ReportRequest_Rule{
			{EventsCount: 1, Payer: client.String()},
		},
	}
	// The call fails at the network, which is fine: the payers are stamped first.
	_, _ = rpc.Report(req)

	if got := req.GetRules()[0].GetPayer(); got != client.String() {
		t.Fatalf("rule payer = %q, want the rule's own %s", got, client)
	}
	if got := req.GetPayer(); got != platform.String() {
		t.Fatalf("request payer = %q, want %s", got, platform)
	}
}

// TestTargetWithoutMatchRulesNeedsNoPayer follows from the payer living on the
// match rule: a request that matches nothing has nobody to bill, so it must not
// require an identity at all.
func TestTargetWithoutMatchRulesNeedsNoPayer(t *testing.T) {
	server := startTestCloud(t, testcloud.Config{ServerID: 1024})
	rpc := newTestClient(server.Addr(), 1)

	resp, sc, err := rpc.Target(&base.TargetRequest{
		Uids: []*base.UID{
			{Id: []byte(uuid.New().String()), Type: base.UID_DEVICE_ID},
		},
	})
	if err != nil {
		t.Fatalf("target: %s: %v", sc, err)
	}
	if len(resp.GetTrackingId()) == 0 {
		t.Fatal("expected a tracking id")
	}
}
