package contract

import (
	"bufio"
	"fmt"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"sync"
)

// Response is a TLV response.
type Response struct {
	value []byte

	sizeBuf [4]byte

	statusCode base.RPCServerResponseCode
}

// SetStatusCode sets the status code of the response to the given RPCServerResponseCode value.
func (req *Response) SetStatusCode(code base.RPCServerResponseCode) {
	req.statusCode = code
}

// GetStatusCode returns the status code of the response.
func (req *Response) GetStatusCode() base.RPCServerResponseCode {
	return req.statusCode
}

// Reset resets the given response.
func (resp *Response) Reset() {
	resp.statusCode = base.RPCServerResponseCode_OK
	resp.value = resp.value[:0]
}

// Write appends p to the response value.
//
// It implements io.Writer.
func (resp *Response) Write(p []byte) (int, error) {
	resp.Append(p)
	return len(p), nil
}

// Append appends p to the response value.
func (resp *Response) Append(p []byte) {
	resp.value = append(resp.value, p...)
}

// SwapValue swaps the given value with the response's value.
//
// It is forbidden accessing the swapped value after the call.
func (resp *Response) SwapValue(value []byte) []byte {
	v := resp.value
	resp.value = value
	return v
}

// Value returns response value.
//
// The returned value is valid until the next Response method call.
// or until ReleaseResponse is called.
func (resp *Response) Value() []byte {
	return resp.value
}

// WriteResponse writes the response to bw.
func (resp *Response) WriteResponse(bw *bufio.Writer) error {
	if err := bw.WriteByte(byte(resp.statusCode)); nil != err {
		return fmt.Errorf("cannot write response status code: %s", err)
	}
	if err := writeBytes(bw, resp.value, resp.sizeBuf[:]); err != nil {
		return fmt.Errorf("cannot write response value: %s", err)
	}
	return nil
}

// ReadResponse reads the response from br.
//
// It implements fastrpc.ReadResponse.
func (resp *Response) ReadResponse(br *bufio.Reader) error {
	var err error
	var sc byte
	if sc, err = br.ReadByte(); nil != err {
		return fmt.Errorf("cannot read response status code: %s", err)
	}
	resp.statusCode = base.RPCServerResponseCode(sc)
	resp.value, err = readBytes(br, resp.value[:0], resp.sizeBuf[:])
	if err != nil {
		return fmt.Errorf("cannot read request value: %s", err)
	}
	return nil
}

// AcquireResponse acquires new response.
func AcquireResponse() *Response {
	v := responsePool.Get()
	if v == nil {
		v = &Response{}
	}
	return v.(*Response)
}

// ReleaseResponse releases the given response.
func ReleaseResponse(resp *Response) {
	resp.Reset()
	responsePool.Put(resp)
}

var responsePool sync.Pool
