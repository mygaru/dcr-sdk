package dcr_sdk

import "sync"

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
