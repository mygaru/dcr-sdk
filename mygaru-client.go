package dcr_sdk

import (
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/valyala/fastjson"
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

func (myg *MyGaru) CheckList(ident string, checkList map[uint32]bool, identType IdentifierType) (map[uint32]bool, error) {
	req := fasthttp.AcquireRequest()
	path := "/segment/touch-multi"
	req.Header.SetBytesV("Authorization", myg.authHeader)

	args := req.URI().QueryArgs()
	for segmentId := range checkList {
		args.SetUint("segment_id", int(segmentId))
	}

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
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		return nil, fmt.Errorf("request failed with status code %d: %s", resp.StatusCode(), resp.Body())
	}

	v, err := fastjson.ParseBytes(resp.Body())
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w: %s", err, resp.Body())
	}

	for segmentId := range checkList {
		r := v.Get(fmt.Sprintf("%d", segmentId))
		if r == nil {
			return nil, fmt.Errorf("segment not found in response: %w: %s", err, resp.Body())
		}

		if errm := r.GetStringBytes("error"); len(errm) > 0 {
			return nil, fmt.Errorf("check unsuccessful: %w: %s", err, errm)
		}

		checkList[segmentId] = r.GetBool("ok")
	}

	return checkList, nil
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
	if err != nil {
		return false, fmt.Errorf("failed to send request: %w", err)
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		return false, fmt.Errorf("request failed with status code %d: %s", resp.StatusCode(), resp.Body())
	}

	v, err := fastjson.ParseBytes(resp.Body())
	if err != nil {
		return false, fmt.Errorf("failed to parse response: %w: %s", err, resp.Body())
	}

	r := v.Get(fmt.Sprintf("%d", segmentId))
	if r == nil {
		return false, fmt.Errorf("segment not found in response: %w: %s", err, resp.Body())
	}

	if errm := r.GetStringBytes("error"); len(errm) > 0 {
		return false, fmt.Errorf("check unsuccessful: %w: %s", err, errm)
	}

	return r.GetBool("ok"), nil
}

func (myg *MyGaru) CheckSegments(ident string, segmentsIds []uint32, identType IdentifierType) (res map[uint32]bool, err error) {
	if len(segmentsIds) == 0 {
		return nil, fmt.Errorf("no segments provided")
	}

	req := fasthttp.AcquireRequest()

	path := "/segment/touch-multi?segment_id=" + strconv.Itoa(int(segmentsIds[0]))

	for _, segmentId := range segmentsIds[1:] {
		path += "&segment_id=" + strconv.Itoa(int(segmentId))
	}

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

	err = myg.client.DoDeadline(req, resp, time.Now().Add(myg.deadlineTimeout))
	if err != nil {
		err = fmt.Errorf("failed to send request: %w", err)
		return
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		err = fmt.Errorf("request failed with status code %d: %s", resp.StatusCode(), resp.Body())
		return
	}

	v, err := fastjson.ParseBytes(resp.Body())
	if err != nil {
		err = fmt.Errorf("failed to parse response: %w: %s", err, resp.Body())
		return
	}

	res = make(map[uint32]bool)

	for _, segmentId := range segmentsIds {
		r := v.Get(fmt.Sprintf("%d", segmentId))
		if r == nil {
			err = fmt.Errorf("segment not found in response: %w: %s", err, resp.Body())
			return
		}

		if errm := r.GetStringBytes("error"); len(errm) > 0 {
			err = fmt.Errorf("check unsuccessful: %w: %s", err, errm)
			return
		}

		res[segmentId] = r.GetBool("ok")
	}

	return
}
