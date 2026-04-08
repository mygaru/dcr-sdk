package contract

import (
	"bufio"
	"fmt"
	"sync"
)

// Request is a TLV request.
type Request struct {
	value   []byte
	code    RPCRegister
	sizeBuf [4]byte
}

// Reset resets the given request.
func (req *Request) Reset() {
	req.code = 0
	req.value = req.value[:0]
}

// SetName sets the RPC code for the request to the provided value.
func (req *Request) SetName(code RPCRegister) {
	req.code = code
}

// GetName returns request name.
//
// The returned value is valid until the next Request method call
// or until ReleaseRequest is called.
func (req *Request) GetName() RPCRegister {
	return req.code
}

// Write appends p to the request value.
//
// It implements io.Writer.
func (req *Request) Write(p []byte) (int, error) {
	req.Append(p)
	return len(p), nil
}

// Append appends p to the request value.
func (req *Request) Append(p []byte) {
	req.value = append(req.value, p...)
}

// SwapValue swaps the given value with the request's value.
//
// It is forbidden accessing the swapped value after the call.
func (req *Request) SwapValue(value []byte) []byte {
	v := req.value
	req.value = value
	return v
}

// Value returns request value.
//
// The returned value is valid until the next Request method call.
// or until ReleaseRequest is called.
func (req *Request) Value() []byte {
	return req.value
}

// WriteRequest writes the request to bw.
//
// It implements fastrpc.RequestWriter
func (req *Request) WriteRequest(bw *bufio.Writer) error {
	if err := bw.WriteByte(byte(req.code)); nil != err {
		return fmt.Errorf("cannot write request code: %s", err)
	}
	if err := writeBytes(bw, req.value, req.sizeBuf[:]); err != nil {
		return fmt.Errorf("cannot write request value: %s", err)
	}
	return nil
}

// ReadRequest reads the request from br.
func (req *Request) ReadRequest(br *bufio.Reader) error {
	var err error
	var rc byte
	if rc, err = br.ReadByte(); nil != err {
		return fmt.Errorf("cannot read request code: %s", err)
	}
	req.code = RPCRegister(rc)

	req.value, err = readBytes(br, req.value[:0], req.sizeBuf[:])
	if err != nil {
		return fmt.Errorf("cannot read request value: %s", err)
	}
	return nil
}

// AcquireRequest acquires new request.
func AcquireRequest() *Request {
	v := requestPool.Get()
	if v == nil {
		v = &Request{}
	}
	return v.(*Request)
}

// ReleaseRequest releases the given request.
func ReleaseRequest(req *Request) {
	req.Reset()
	requestPool.Put(req)
}

var requestPool sync.Pool
