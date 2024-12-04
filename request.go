package dcr_sdk

import (
	"fmt"
	"github.com/aradilov/batcher"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fastjson"
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
			requests[task.Req.uid] = newRequest(task.Req.uid, task.Req.identType, myg.profileId, task.Req.segmentId)
		} else {
			req.URI().QueryArgs().Add("segmentId", fmt.Sprintf("%d", task.Req.segmentId))
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
				task.Done(fmt.Errorf("not found segmentID in response"))
				continue
			}

			if r.GetBool("success") {
				task.Res = r.GetBool("ok")
				task.Done(nil)
			} else {
				task.Done(fmt.Errorf("%s", r.GetStringBytes("error")))
			}
		}

		fasthttp.ReleaseResponse(resp)
		fasthttp.ReleaseRequest(req)
	}
}

func newRequest(uid string, identifierType IdentifierType, clientId, segmentId uint32) *fasthttp.Request {
	req := fasthttp.AcquireRequest()

	path := fmt.Sprintf("/segments/check?clientId=%d&segmentId=%d", clientId, segmentId)

	switch identifierType {
	case IdentifierTypeExternal:
		path += fmt.Sprintf("&externalUID=%s", uid)
	case IdentifierTypeOTP:
		path += fmt.Sprintf("&otp=%s", uid)
	default:
		path += fmt.Sprintf("&otp=%s", uid)
	}

	req.SetRequestURI(baseURI + path)
	req.Header.SetMethod(fasthttp.MethodGet)
	return req
}
