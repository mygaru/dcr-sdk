package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	base "github.com/mygaru/dcr-sdk/gen/base1"
)

func TestRPCOutcome(t *testing.T) {
	cases := []struct {
		status base.RPCServerResponseCode
		err    error
		want   Outcome
	}{
		{base.RPCServerResponseCode_OK, nil, Pass},
		// the handler answered: whatever it said, the transport worked
		{base.RPCServerResponseCode_INVALID_REQUEST, errors.New("tracking_id not found"), Pass},
		{base.RPCServerResponseCode_TECH_ERROR, errors.New("boom"), Pass},
		// answered, but the identity was refused
		{base.RPCServerResponseCode_UNAUTHORIZED, errors.New("no"), Warn},
		{base.RPCServerResponseCode_FORBIDDEN, errors.New("touch not granted"), Warn},
		// no answer at all: port, handshake or certificate
		{base.RPCServerResponseCode_NETWORK_ERROR, errors.New("connection reset"), Fail},
		{base.RPCServerResponseCode_UNKNOWN, errors.New("marshal"), Fail},
	}

	for _, c := range cases {
		got, _ := rpcOutcome(c.status, c.err)
		if got != c.want {
			t.Errorf("rpcOutcome(%s) = %s, want %s", c.status, got, c.want)
		}
	}
}

func TestHTTPOutcome(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   Outcome
	}{
		{200, `{"ok": false}`, Pass},
		{400, "cannot unmarshal target request", Pass},
		{404, "segment not found", Pass}, // the handler's own 404
		{404, "Unsupported http path", Fail},
		{403, "Not served by a read-only Cloud", Fail},
		{403, "user auth failed", Fail},
		{401, "failed to validate OAuth token", Fail},
		{502, "", Fail},
		{503, "", Fail},
		{504, "", Fail},
		{500, "cannot marshal pending report context", Warn},
	}

	for _, c := range cases {
		got, _ := httpOutcome(c.status, []byte(c.body))
		if got != c.want {
			t.Errorf("httpOutcome(%d, %q) = %s, want %s", c.status, c.body, got, c.want)
		}
	}
}

// fakeRPC answers the RPC checks and records what they sent.
type fakeRPC struct {
	targetResp   *base.TargetResponse
	targetStatus base.RPCServerResponseCode
	targetErr    error

	reports []*base.ReportRequest
}

func (f *fakeRPC) Target(*base.TargetRequest) (*base.TargetResponse, base.RPCServerResponseCode, error) {
	return f.targetResp, f.targetStatus, f.targetErr
}

func (f *fakeRPC) Report(req *base.ReportRequest) (base.RPCServerResponseCode, error) {
	f.reports = append(f.reports, req)
	return base.RPCServerResponseCode_OK, nil
}

func (f *fakeRPC) Touch(*base.TouchRequest) (*base.TouchResponse, base.RPCServerResponseCode, error) {
	return nil, base.RPCServerResponseCode_FORBIDDEN, errors.New("touch is not granted")
}

func outcomes(results []Result) map[string]Outcome {
	m := make(map[string]Outcome, len(results))
	for _, r := range results {
		m[r.Name] = r.Outcome
	}
	return m
}

func TestRunRPCChecksReportsTheTargetsTrackingID(t *testing.T) {
	tracking := []byte("0123456789abcdef")
	f := &fakeRPC{targetResp: &base.TargetResponse{TrackingId: tracking}, targetStatus: base.RPCServerResponseCode_OK}

	results := runRPCChecks(f, "11111111-1111-1111-1111-111111111111", &base.UID{Id: []byte("d"), Type: base.UID_DEVICE_ID})

	want := map[string]Outcome{"rpc.target": Pass, "rpc.report": Pass, "rpc.touch": Warn}
	if got := outcomes(results); !equalOutcomes(got, want) {
		t.Fatalf("outcomes = %v, want %v", got, want)
	}

	if len(f.reports) != 1 {
		t.Fatalf("sent %d reports, want 1", len(f.reports))
	}

	report := f.reports[0]
	if !bytes.Equal(report.GetTrackingId(), tracking) {
		t.Errorf("report tracking id = %x, want the target's %x", report.GetTrackingId(), tracking)
	}
	if rules := report.GetRules(); len(rules) != 1 || len(rules[0].GetSegmentIds()) != 0 || rules[0].GetEventsCount() == 0 {
		t.Errorf("report rules = %v, want one rule with events and no segments", rules)
	}
}

func TestRunRPCChecksReportsWithoutATrackingID(t *testing.T) {
	f := &fakeRPC{targetStatus: base.RPCServerResponseCode_NETWORK_ERROR, targetErr: errors.New("dial timeout")}

	results := runRPCChecks(f, "11111111-1111-1111-1111-111111111111", &base.UID{Id: []byte("d"), Type: base.UID_DEVICE_ID})

	if got := outcomes(results)["rpc.target"]; got != Fail {
		t.Errorf("rpc.target = %s, want FAIL", got)
	}
	if len(f.reports) != 1 || !bytes.Equal(f.reports[0].GetTrackingId(), make([]byte, 16)) {
		t.Errorf("report should still go out, with a zero tracking id")
	}
}

func TestRunRPCChecksWithoutPayer(t *testing.T) {
	f := &fakeRPC{targetResp: &base.TargetResponse{}, targetStatus: base.RPCServerResponseCode_OK}

	results := runRPCChecks(f, "", &base.UID{Id: []byte("d"), Type: base.UID_DEVICE_ID})

	if got := outcomes(results)["rpc.report"]; got != Fail {
		t.Errorf("rpc.report = %s, want FAIL", got)
	}
	if len(f.reports) != 0 {
		t.Errorf("a report without a payer was sent")
	}
}

func TestRunHTTPChecks(t *testing.T) {
	tracking := []byte("fedcba9876543210")

	var reportBody []byte
	var touchQuery string

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "failed to validate OAuth token", http.StatusUnauthorized)
			return
		}

		switch r.URL.Path {
		case "/dcr/test-target":
			body, _ := protojson.Marshal(&base.TargetResponse{TrackingId: tracking})
			_, _ = w.Write(body)
		case "/dcr/test-report":
			reportBody, _ = io.ReadAll(r.Body)
		case "/segment/touch":
			touchQuery = r.URL.RawQuery
			http.Error(w, "segment not found", http.StatusNotFound)
		default:
			http.Error(w, "Unsupported http path", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := &httpChecker{client: srv.Client(), baseURL: srv.URL, token: "tok"}

	results := runHTTPChecks(c, "device-1", 0)

	want := map[string]Outcome{"http.target": Pass, "http.report": Pass, "http.touch": Pass}
	if got := outcomes(results); !equalOutcomes(got, want) {
		t.Fatalf("outcomes = %v, want %v (%+v)", got, want, results)
	}

	report := &base.ReportRequest{}
	if err := protojson.Unmarshal(reportBody, report); err != nil {
		t.Fatalf("report body: %v", err)
	}
	if !bytes.Equal(report.GetTrackingId(), tracking) {
		t.Errorf("report tracking id = %x, want the target's %x", report.GetTrackingId(), tracking)
	}
	if report.GetPayer() != "" {
		t.Errorf("HTTP report names payer %q; the token is the identity there", report.GetPayer())
	}

	if touchQuery != "device_id=device-1&segment_id=0&traffic_type=display" {
		t.Errorf("touch query = %q", touchQuery)
	}
}

func TestRunHTTPChecksWithARefusedToken(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "failed to validate OAuth token", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := &httpChecker{client: srv.Client(), baseURL: srv.URL, token: "bad"}

	for _, r := range runHTTPChecks(c, "device-1", 0) {
		if r.Outcome != Fail {
			t.Errorf("%s = %s, want FAIL", r.Name, r.Outcome)
		}
	}
}

func TestExitCode(t *testing.T) {
	warned := []Result{{Outcome: Pass}, {Outcome: Warn}, {Outcome: Skip}}
	if exitCode(warned, false) != 0 || exitCode(warned, true) != 1 {
		t.Errorf("a warning fails only with -strict")
	}

	if exitCode([]Result{{Outcome: Pass}, {Outcome: Fail}}, false) != 1 {
		t.Errorf("a failure always fails")
	}
}

func equalOutcomes(a, b map[string]Outcome) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
