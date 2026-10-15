package main

import (
	"fmt"
	"time"

	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/pkg/client"
)

// rpcClient is the part of the SDK client the RPC checks use; tests stand in
// for it.
type rpcClient interface {
	Target(req *base.TargetRequest) (*base.TargetResponse, base.RPCServerResponseCode, error)
	Report(req *base.ReportRequest) (base.RPCServerResponseCode, error)
	Touch(req *base.TouchRequest) (*base.TouchResponse, base.RPCServerResponseCode, error)
}

var _ rpcClient = (*client.ShardedClient)(nil)

// rpcOutcome judges one RPC call.
//
// The client reports a request that got no answer - no connection, a refused
// handshake, a timeout - as NETWORK_ERROR, and one it could not even build as
// UNKNOWN. Anything else is a status the cloud's handler chose, so the port,
// TLS and the client certificate all worked: the mTLS handshake happens before
// the first request, and the cloud closes a connection whose certificate it
// refuses. A refusal of the identity behind the certificate is a status too,
// and is a warning rather than a failure.
func rpcOutcome(status base.RPCServerResponseCode, err error) (Outcome, string) {
	if err == nil {
		return Pass, status.String()
	}

	switch status {
	case base.RPCServerResponseCode_NETWORK_ERROR, base.RPCServerResponseCode_UNKNOWN:
		return Fail, err.Error()
	case base.RPCServerResponseCode_UNAUTHORIZED, base.RPCServerResponseCode_FORBIDDEN:
		return Warn, fmt.Sprintf("answered %s: %v", status, err)
	default:
		return Pass, fmt.Sprintf("answered %s: %v", status, err)
	}
}

// runRPCChecks sends one Target, one Report and one Touch.
//
// The Report settles the Target's tracking id with a rule that names no
// segment, so it bills nothing; the identifier is a random device id, so no
// OTP is decrypted either. When the Target got no tracking id the Report still
// goes out, with an id of zeros that no node owns: it is answered INVALID_REQUEST
// locally, which is all the check needs.
func runRPCChecks(cli rpcClient, payer string, uid *base.UID) []Result {
	var results []Result

	var trackingID []byte

	st := time.Now()
	resp, status, err := cli.Target(&base.TargetRequest{Uids: []*base.UID{uid}})
	outcome, detail := rpcOutcome(status, err)
	if err == nil {
		trackingID = resp.GetTrackingId()
		detail = fmt.Sprintf("%s, tracking_id=%s", detail, trackingID)
	}
	results = append(results, Result{Name: "rpc.target", Outcome: outcome, Detail: detail, Took: time.Since(st)})

	if len(trackingID) == 0 {
		trackingID = make([]byte, 16)
	}

	// The SDK refuses a Report without a payer before sending it, with a status
	// that would read as the cloud's answer.
	if payer == "" {
		results = append(results, Result{Name: "rpc.report", Outcome: Fail, Detail: "no payer: the certificate names no partner, pass -payer"})
	} else {
		results = append(results, sendReport(cli, payer, trackingID))
	}

	st = time.Now()
	touch, status, err := cli.Touch(&base.TouchRequest{Uids: []*base.UID{uid}})
	outcome, detail = rpcOutcome(status, err)
	if err == nil {
		detail = fmt.Sprintf("%s, accuracy=%s", detail, touch.GetAccuracy())
	}
	results = append(results, Result{Name: "rpc.touch", Outcome: outcome, Detail: detail, Took: time.Since(st)})

	return results
}

func sendReport(cli rpcClient, payer string, trackingID []byte) Result {
	st := time.Now()
	status, err := cli.Report(&base.ReportRequest{
		TrackingId: trackingID,
		Payer:      payer,
		Event:      base.EventType_EVENT_TYPE_IMPRESSION,
		Rules:      []*base.ReportRequest_Rule{zeroBillingReportRule()},
	})
	outcome, detail := rpcOutcome(status, err)

	return Result{Name: "rpc.report", Outcome: outcome, Detail: detail, Took: time.Since(st)}
}

// zeroBillingReportRule is a valid report rule that bills nothing: the cloud
// emits one billing row per segment of a rule, and this one has none.
func zeroBillingReportRule() *base.ReportRequest_Rule {
	return &base.ReportRequest_Rule{
		TrafficType: base.TrafficType_TRAFFIC_TYPE_DISPLAY,
		EventsCount: 1,
	}
}
