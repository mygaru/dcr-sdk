package dcr_sdk

import (
	"fmt"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fastjson"
	"net/url"
	"time"
)

type MyGaru struct {
	authHeader      []byte
	deadlineTimeout time.Duration
	client          *fasthttp.Client
}

const (
	baseURI = "https://cloud.mgaru.dev"
	// minimum nr identifiers for a scan request

)

type IdentifierType uint8

const (
	IdentifierTypePartnerUID IdentifierType = iota
	IdentifierTypeOTP
	IdentifierTypeDeviceID
	IdentifierTypeExternalUID
)

func Init(token []byte, deadlineTimeout, _ time.Duration, _ int) *MyGaru {
	authHeader := []byte("Bearer ")
	authHeader = append(authHeader, token...)

	myg := &MyGaru{
		authHeader: authHeader,
		client: &fasthttp.Client{
			ReadTimeout:         30 * time.Second,
			WriteTimeout:        30 * time.Second,
			ReadBufferSize:      4 * 1024,
			MaxIdleConnDuration: time.Hour,
			MaxConnsPerHost:     10e3,
			MaxResponseBodySize: 1024 * 1024, // 1Kb
		},
		deadlineTimeout: deadlineTimeout,
	}

	return myg
}

// Check checks whether an identifier is in a segment.
func (myg *MyGaru) Check(ident string, segmentId uint32, identType IdentifierType) (bool, error) {
	req := fasthttp.AcquireRequest()
	path := fmt.Sprintf("/segment/touch-multi?segment_id=%d", segmentId)
	req.Header.SetBytesV("Authorization", myg.authHeader)
	ident = url.QueryEscape(ident)

	switch identType {
	case IdentifierTypePartnerUID:
		path += fmt.Sprintf("&partner_uid=%s", ident)
	case IdentifierTypeOTP:
		path += fmt.Sprintf("&otp=%s", ident)
	case IdentifierTypeDeviceID:
		path += fmt.Sprintf("&device_id=%s", ident)
	case IdentifierTypeExternalUID:
		path += fmt.Sprintf("&external_uid=%s", ident)
	default:
		path += fmt.Sprintf("&otp=%s", ident)
	}

	req.SetRequestURI(baseURI + path)
	resp := fasthttp.AcquireResponse()

	req.Header.SetMethod(fasthttp.MethodGet)

	defer func() {
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)
	}()

	err := myg.client.DoDeadline(req, resp, time.Now().Add(myg.deadlineTimeout))

	if resp.StatusCode() != fasthttp.StatusOK {
		return false, fmt.Errorf("request failed with status code %d: %s", resp.StatusCode(), resp.Body())
	}

	v, err := fastjson.ParseBytes(resp.Body())
	if err != nil {
		return false, fmt.Errorf("failed to parse response: %s: %s", err, resp.Body())
	}

	r := v.Get(fmt.Sprintf("%d", segmentId))
	if r == nil {
		return false, fmt.Errorf("segment not found in response: %s: %s", err, resp.Body())
	}

	if errm := r.GetStringBytes("error"); len(errm) > 0 {
		return false, fmt.Errorf("check unsuccessful: %s: %s", err, errm)
	}

	return r.GetBool("ok"), nil
}
