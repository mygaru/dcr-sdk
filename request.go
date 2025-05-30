package dcr_sdk

import (
	"fmt"
	"github.com/aradilov/batcher"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fastjson"
	"net/url"
	"sync"
	"time"
)

type Request struct {
	uid       string
	identType IdentifierType
	segmentId uint32
}

var reqPool sync.Pool

func acquireRequest() *Request {
	v := reqPool.Get()
	if v == nil {
		return &Request{}
	}
	return v.(*Request)
}

func releaseRequest(a *Request) {
	reqPool.Put(a)
}

func (myg *MyGaru) processBatch(tasks []*batcher.Task[*Request, bool]) {
	requests := make(map[string]*fasthttp.Request)
	uidToTasks := make(map[string][]*batcher.Task[*Request, bool])

	for _, task := range tasks {
		req, ok := requests[task.Req.uid]
		if !ok {
			requests[task.Req.uid] = newRequest(task.Req.uid, task.Req.identType, task.Req.segmentId, myg.authHeader)
		} else {
			req.URI().QueryArgs().Add("segment_id", fmt.Sprintf("%d", task.Req.segmentId))
		}

		uidToTasks[task.Req.uid] = append(uidToTasks[task.Req.uid], task)
	}

	for uid, req := range requests {
		resp := fasthttp.AcquireResponse()
		err := myg.client.DoDeadline(req, resp, time.Now().Add(myg.deadlineTimeout))
		if err != nil {
			for _, task := range uidToTasks[uid] {
				task.Done(err)
			}
		}

		if resp.StatusCode() != fasthttp.StatusOK {
			for _, task := range uidToTasks[uid] {
				task.Done(fmt.Errorf("request failed with status code %d: %s", resp.StatusCode(), resp.Body()))
			}
		}

		v, err := fastjson.ParseBytes(resp.Body())
		if err != nil {
			for _, task := range uidToTasks[uid] {
				task.Done(err)
			}
		}

		// Match the response or error to the original tasks
		for _, task := range uidToTasks[uid] {
			if err != nil {
				task.Done(err)
				continue
			}

			r := v.Get(fmt.Sprintf("%d", task.Req.segmentId))
			if r == nil {
				task.Done(fmt.Errorf("segment not found in response"))
				continue
			}

			if err := r.GetStringBytes("error"); err != nil {
				task.Done(fmt.Errorf("check unsuccessful: %s", err))
				continue
			}

			task.Res = r.GetBool("ok")
			task.Done(nil)
		}

		fasthttp.ReleaseResponse(resp)
		fasthttp.ReleaseRequest(req)
	}
}

func newRequest(ident string, identifierType IdentifierType, segmentId uint32, authHeader []byte) *fasthttp.Request {
	req := fasthttp.AcquireRequest()
	path := fmt.Sprintf("/segment/touch-multi?segment_id=%d", segmentId)
	req.Header.SetBytesV("Authorization", authHeader)
	ident = url.QueryEscape(ident)

	switch identifierType {
	case IdentifierTypePartnerUID:
		path += fmt.Sprintf("&partner_uid=%s", ident)
	case IdentifierTypeOTP:
		path += fmt.Sprintf("&otp=%s", ident)
	case IdentifierTypeDeviceID:
		path += fmt.Sprintf("&device_id=%s", ident)
	default:
		path += fmt.Sprintf("&otp=%s", ident)
	}

	req.SetRequestURI(baseURI + path)
	req.Header.SetMethod(fasthttp.MethodGet)
	return req
}
