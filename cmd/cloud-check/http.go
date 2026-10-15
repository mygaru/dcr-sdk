package main

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	base "github.com/mygaru/dcr-sdk/gen/base1"
)

// The bodies the cloud answers with when a request never reaches a handler.
// Matched on to tell "this route is not served here" apart from a handler that
// answered 403 or 404 itself.
const (
	bodyUnsupportedPath = "Unsupported http path"
	bodyReadOnlyRefusal = "read-only Cloud"
)

// httpChecker sends the HTTP transport's requests: protojson bodies for
// Target and Report, query arguments for Touch, the OAuth token in every one.
type httpChecker struct {
	client  *http.Client
	baseURL string
	token   string
}

// httpOutcome judges one HTTP exchange.
//
// The HTTP transport authenticates by OAuth token, not by client certificate,
// so a refused token is the failure here that a refused certificate is on RPC.
// So is a route the node does not serve, and a gateway error: neither is the
// cloud answering. A 500 is the handler answering that it failed, which the
// setup check only warns about. Everything else - 200, 400, a 404 for an
// unknown segment - is the handler having taken the request.
func httpOutcome(status int, body []byte) (Outcome, string) {
	text := strings.TrimSpace(string(body))
	detail := fmt.Sprintf("HTTP %d", status)
	if text != "" {
		detail += ": " + text
	}

	switch {
	case status == http.StatusNotFound && strings.Contains(text, bodyUnsupportedPath):
		return Fail, detail + " (route not served by this node)"
	case status == http.StatusForbidden && strings.Contains(text, bodyReadOnlyRefusal):
		return Fail, detail + " (route refused by a read-only Cloud)"
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return Fail, detail + " (token refused)"
	case status == http.StatusBadGateway, status == http.StatusServiceUnavailable, status == http.StatusGatewayTimeout:
		return Fail, detail + " (gateway: the cloud did not answer)"
	case status >= 500:
		return Warn, detail
	default:
		return Pass, detail
	}
}

// do sends one request and judges what came back. It also returns the body, for
// a caller that needs something out of a successful answer.
func (c *httpChecker) do(name string, req *http.Request) (Result, []byte) {
	req.Header.Set("Authorization", "Bearer "+c.token)

	st := time.Now()
	resp, err := c.client.Do(req)
	if err != nil {
		return Result{Name: name, Outcome: Fail, Detail: err.Error(), Took: time.Since(st)}, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	took := time.Since(st)
	if err != nil {
		return Result{Name: name, Outcome: Fail, Detail: fmt.Sprintf("HTTP %d, reading body: %v", resp.StatusCode, err), Took: took}, nil
	}

	outcome, detail := httpOutcome(resp.StatusCode, body)
	if days, ok := serverCertDaysLeft(resp.TLS); ok {
		detail += fmt.Sprintf(" [server cert: %d days left]", days)
	}

	return Result{Name: name, Outcome: outcome, Detail: detail, Took: took}, body
}

func (c *httpChecker) postProto(name, path string, msg proto.Message) (Result, []byte) {
	body, err := protojson.Marshal(msg)
	if err != nil {
		return Result{Name: name, Outcome: Fail, Detail: fmt.Sprintf("marshal request: %v", err)}, nil
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return Result{Name: name, Outcome: Fail, Detail: err.Error()}, nil
	}
	req.Header.Set("Content-Type", "application/json")

	return c.do(name, req)
}

// runHTTPChecks sends one Target, one Report and one Touch over HTTP, with the
// same zero-billing Report as the RPC checks (see runRPCChecks).
//
// touchSegmentID may be zero: the token is checked before the segment, so the
// cloud's "no such segment" still proves the route and the token.
func runHTTPChecks(c *httpChecker, deviceID string, touchSegmentID uint) []Result {
	var results []Result

	uid := &base.UID{Id: []byte(deviceID), Type: base.UID_DEVICE_ID}

	target, body := c.postProto("http.target", "/dcr/test-target", &base.TargetRequest{Uids: []*base.UID{uid}})

	trackingID := make([]byte, 16)
	if target.Outcome == Pass && len(body) > 0 {
		resp := &base.TargetResponse{}
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(body, resp); err == nil && len(resp.GetTrackingId()) > 0 {
			trackingID = resp.GetTrackingId()
			target.Detail += ", tracking_id=" + string(trackingID)
		}
	}
	results = append(results, target)

	// The payer is the token's partner on this transport, so the request names none.
	report, _ := c.postProto("http.report", "/dcr/test-report", &base.ReportRequest{
		TrackingId: trackingID,
		Event:      base.EventType_EVENT_TYPE_IMPRESSION,
		Rules:      []*base.ReportRequest_Rule{zeroBillingReportRule()},
	})
	results = append(results, report)

	query := url.Values{}
	query.Set("device_id", deviceID)
	query.Set("traffic_type", "display")
	query.Set("segment_id", fmt.Sprint(touchSegmentID))

	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/segment/touch?"+query.Encode(), nil)
	if err != nil {
		results = append(results, Result{Name: "http.touch", Outcome: Fail, Detail: err.Error()})
		return results
	}

	touch, _ := c.do("http.touch", req)
	results = append(results, touch)

	return results
}

func serverCertDaysLeft(state *tls.ConnectionState) (int, bool) {
	if state == nil || len(state.PeerCertificates) == 0 {
		return 0, false
	}

	return int(time.Until(state.PeerCertificates[0].NotAfter).Hours() / 24), true
}
