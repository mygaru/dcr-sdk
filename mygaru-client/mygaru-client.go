package mygaru_client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/valyala/fasthttp"
	"io"
	"strings"
	"time"
)

type MyGaru struct {
	profileId uint32
	client    *fasthttp.Client
}

const (
	baseURI = "https://cloud.mgaru.dev"
	// minimum nr identifiers for a scan request
	scanUIDMinLimit = 100
)

type IdentifierType uint8

const (
	IdentifierTypeExternal IdentifierType = iota
	IdentifierTypeOTP
)

func Init(profileId uint32, readTimeout time.Duration) *MyGaru {
	return &MyGaru{
		profileId: profileId,
		client: &fasthttp.Client{
			Name:        fmt.Sprintf("mygaru-client-%d", profileId),
			ReadTimeout: readTimeout,
		},
	}
}

type checkResult struct {
	OK bool `json:"ok"`
}

// Check checks whether an identifier is in a segment.
func (myg *MyGaru) Check(uid string, segmentId uint32, identType IdentifierType) (bool, error) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()

	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	path := fmt.Sprintf("/segments/check?segmentId=%d&clientId=%d", segmentId, myg.profileId)

	switch identType {
	case IdentifierTypeExternal:
		path += fmt.Sprintf("&externalUID=%s", uid)
	case IdentifierTypeOTP:
		path += fmt.Sprintf("&otp=%s", uid)
	default:
		path += fmt.Sprintf("&otp=%s", uid)
	}

	req.SetRequestURI(baseURI + path)
	req.Header.SetMethod(fasthttp.MethodGet)

	err := myg.client.Do(req, resp)
	if err != nil {
		return false, err
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		return false, fmt.Errorf("%d: %s", resp.StatusCode(), resp.Body())
	}

	var checkResult checkResult
	err = json.Unmarshal(resp.Body(), &checkResult)

	if err != nil {
		return false, err
	}

	return checkResult.OK, nil
}

type scanResult struct {
	Intersection float32 `json:"intersection"`
}

// Scan returns the percentage of identifiers contained in some segment.
func (myg *MyGaru) Scan(uids []string, segmentId uint32) (float32, error) {
	if len(uids) < scanUIDMinLimit {
		return 0.0, fmt.Errorf("please input at least %d uids", scanUIDMinLimit)
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()

	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	path := fmt.Sprintf("/segments/scan?segmentId=%d&clientId=%d", segmentId, myg.profileId)

	req.SetRequestURI(baseURI + path)
	req.Header.SetMethod(fasthttp.MethodPost)

	req.SetBodyString(strings.Join(uids, ",\n"))

	err := myg.client.Do(req, resp)
	if err != nil {
		return 0, err
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		return 0, fmt.Errorf("%d: %s", resp.StatusCode(), resp.Body())
	}

	var scanResult scanResult
	err = json.Unmarshal(resp.Body(), &scanResult)

	if err != nil {
		return 0, err
	}

	return scanResult.Intersection, nil
}

// ScanBytes returns the percentage of identifiers contained in some segment.
// uids must be list of identifiers separated by ",\n"
func (myg *MyGaru) ScanBytes(uids []byte, segmentId uint32) (float32, error) {
	cnt := bytes.Count(uids, []byte(","))
	if cnt < scanUIDMinLimit {
		return 0.0, fmt.Errorf("please input at least %d uids", scanUIDMinLimit)
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()

	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	path := fmt.Sprintf("/segments/scan?segmentId=%d&clientId=%d", segmentId, myg.profileId)

	req.SetRequestURI(baseURI + path)
	req.Header.SetMethod(fasthttp.MethodPost)

	req.SetBody(uids)

	err := myg.client.Do(req, resp)
	if err != nil {
		return 0, err
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		return 0, fmt.Errorf("%d: %s", resp.StatusCode(), resp.Body())
	}

	var scanResult scanResult
	err = json.Unmarshal(resp.Body(), &scanResult)

	if err != nil {
		return 0, err
	}

	return scanResult.Intersection, nil
}

// ScanReader returns the percentage of identifiers contained in some segment.
// Use to scan uids from files, nework responses, etc.
func (myg *MyGaru) ScanReader(reader io.Reader, segmentId uint32) (float32, error) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()

	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	path := fmt.Sprintf("/segments/scan?segmentId=%d&clientId=%d", segmentId, myg.profileId)

	req.SetRequestURI(baseURI + path)
	req.Header.SetMethod(fasthttp.MethodPost)

	req.SetBodyStream(reader, -1)

	err := myg.client.Do(req, resp)
	if err != nil {
		return 0, err
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		return 0, fmt.Errorf("%d: %s", resp.StatusCode(), resp.Body())
	}

	var scanResult scanResult
	err = json.Unmarshal(resp.Body(), &scanResult)

	if err != nil {
		return 0, err
	}

	return scanResult.Intersection, nil
}
