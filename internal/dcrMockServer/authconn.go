package dcrMockServer

import (
	"log"
	"net"
	"sync/atomic"

	"github.com/google/uuid"
)

type authConn struct {
	net.Conn

	Requests atomic.Uint64

	authId uuid.UUID
}

func (c *authConn) GetUUID() uuid.UUID {
	return c.authId
}

func (c *authConn) SetUUID(uid uuid.UUID) {
	c.authId = uid
}

type customListener struct {
	net.Listener
}

func (ln *customListener) Accept() (c net.Conn, err error) {

	c, err = ln.Listener.Accept()
	if err != nil {
		if c != nil {
			log.Panicf("BUG: accept returned non-nil c=%#v with error %s", c, err)
		}
		return nil, err
	}

	return &authConn{Conn: c}, nil
}
