package dcr_sdk

import (
	"bytes"
	"fmt"
	"github.com/aradilov/batcher"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fastjson"
	"gitlab.adtelligent.com/common/shared/osexit"
	"io"
	"os"
	"strings"
	"time"
)

type MyGaru struct {
	profileId       uint32
	deadlineTimeout time.Duration
	client          *fasthttp.Client
	batcher         *batcher.GenericBatcherTask[*Request, bool]
}

const (
	baseURI = "https://cloud.mgaru.dev"
	// minimum nr identifiers for a scan request
	scanUIDMinLimit = 100
)

type IdentifierType uint8

const (
	IdentifierTypePartnerUID IdentifierType = iota
	IdentifierTypeOTP
	IdentifierTypeDeviceID
)

func Init(profileId uint32, deadlineTimeout, batchTimeout time.Duration, batchSize int) *MyGaru {
	myg := &MyGaru{
		profileId: profileId,
		client: &fasthttp.Client{
			MaxConnsPerHost:     5000,
			ReadTimeout:         3 * time.Second,
			WriteTimeout:        3 * time.Second,
			ReadBufferSize:      16 * 1024,
			MaxIdleConnDuration: 60 * time.Second,
			MaxResponseBodySize: 1024 * 1024, // 1Kb
		},
		batcher: &batcher.GenericBatcherTask[*Request, bool]{
			MaxBatchSize: batchSize,
			QueueSize:    3 * batchSize,
			MaxDelay:     batchTimeout,
		},
		deadlineTimeout: deadlineTimeout,
	}

	myg.batcher.Func = myg.processBatch
	myg.batcher.Start()

	osexit.Before(func(signal os.Signal) {
		myg.batcher.Stop()
	})

	return myg
}

// Check checks whether an identifier is in a segment.
func (myg *MyGaru) Check(ident string, segmentId uint32, identType IdentifierType) (bool, error) {
	r := acquireRequest()
	r.uid = ident
	r.identType = identType
	r.segmentId = segmentId

	ok, err := myg.batcher.Do(r)
	releaseRequest(r)
	return ok, err
}

// Scan returns the percentage of identifiers contained in some segment.
func (myg *MyGaru) Scan(puids []string, segmentId uint32) (float32, error) {
	if len(puids) < scanUIDMinLimit {
		return 0.0, fmt.Errorf("please input at least %d puids", scanUIDMinLimit)
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()

	defer func() {
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)
	}()

	path := fmt.Sprintf("/segment/scan?segment_id=%d&client_id=%d", segmentId, myg.profileId)

	req.SetRequestURI(baseURI + path)
	req.Header.SetMethod(fasthttp.MethodPost)

	req.SetBodyString(strings.Join(puids, ",\n"))

	err := myg.client.DoDeadline(req, resp, time.Now().Add(myg.deadlineTimeout))
	if err != nil {
		return 0, err
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		return 0, fmt.Errorf("request to host = %q  failed with code %d: %s", req.URI().String(), resp.StatusCode(), resp.Body())
	}

	inter := fastjson.GetFloat64(resp.Body(), "intersection")
	return float32(inter), nil
}

// ScanBytes returns the percentage of identifiers contained in some segment.
// puids must be list of identifiers separated by ",\n"
func (myg *MyGaru) ScanBytes(puids []byte, segmentId uint32) (float32, error) {
	cnt := bytes.Count(puids, []byte(","))
	if cnt < scanUIDMinLimit-1 {
		return 0.0, fmt.Errorf("please input at least %d puids", scanUIDMinLimit)
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()

	defer func() {
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)
	}()

	path := fmt.Sprintf("/segment/scan?segment_id=%d&client_id=%d", segmentId, myg.profileId)

	req.SetRequestURI(baseURI + path)
	req.Header.SetMethod(fasthttp.MethodPost)

	req.SetBody(puids)

	err := myg.client.DoDeadline(req, resp, time.Now().Add(myg.deadlineTimeout))
	if err != nil {
		return 0, err
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		return 0, fmt.Errorf("request to host = %q  failed with code %d: %s", req.URI().String(), resp.StatusCode(), resp.Body())
	}

	inter := fastjson.GetFloat64(resp.Body(), "intersection")
	return float32(inter), nil
}

// ScanReader returns the percentage of identifiers contained in some segment.
// Use to scan puids from files, nework responses, etc.
func (myg *MyGaru) ScanReader(reader io.Reader, segmentId uint32) (float32, error) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()

	defer func() {
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)
	}()

	path := fmt.Sprintf("/segment/scan?segment_id=%d&client_id=%d", segmentId, myg.profileId)

	req.SetRequestURI(baseURI + path)
	req.Header.SetMethod(fasthttp.MethodPost)

	req.SetBodyStream(reader, -1)

	err := myg.client.DoDeadline(req, resp, time.Now().Add(myg.deadlineTimeout))
	if err != nil {
		return 0, err
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		return 0, fmt.Errorf("request to host = %q  failed with code %d: %s", req.URI().String(), resp.StatusCode(), resp.Body())
	}

	inter := fastjson.GetFloat64(resp.Body(), "intersection")
	return float32(inter), nil
}
