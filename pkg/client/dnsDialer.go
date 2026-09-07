package client

import (
	"context"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/valyala/fasthttp"
)

const defaultDNSRefreshInterval = time.Minute

type dnsResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

type dnsDialer struct {
	addr            string
	host            string
	port            string
	refreshInterval time.Duration
	dialTimeout     time.Duration
	resolver        dnsResolver

	next atomic.Uint64

	mu    sync.Mutex
	addrs []string
	conns map[*trackedConn]string
}

func newDNSDialer(addr string, dialTimeout, refreshInterval time.Duration) *dnsDialer {
	d := &dnsDialer{
		addr:            addr,
		refreshInterval: refreshInterval,
		dialTimeout:     dialTimeout,
		resolver:        net.DefaultResolver,
		conns:           make(map[*trackedConn]string),
	}
	d.init()
	return d
}

func newDNSDialerWithResolver(addr string, dialTimeout, refreshInterval time.Duration, resolver dnsResolver) *dnsDialer {
	d := &dnsDialer{
		addr:            addr,
		refreshInterval: refreshInterval,
		dialTimeout:     dialTimeout,
		resolver:        resolver,
		conns:           make(map[*trackedConn]string),
	}
	d.init()
	return d
}

func (d *dnsDialer) init() {
	host, port, err := net.SplitHostPort(d.addr)
	if err != nil {
		d.addrs = []string{d.addr}
		return
	}
	d.host = host
	d.port = port

	addrs := d.resolve()
	if len(addrs) == 0 {
		addrs = []string{d.addr}
	}
	d.addrs = addrs

	if d.refreshInterval > 0 && net.ParseIP(host) == nil {
		go d.refreshLoop()
	}
}

func (d *dnsDialer) dial(owner *client) (net.Conn, error) {
	addr := d.nextAddr()
	conn, err := fasthttp.DialTimeout(addr, d.dialTimeout)
	if err != nil {
		return nil, err
	}

	tc := &trackedConn{
		Conn:   conn,
		dialer: d,
		addr:   addr,
		owner:  owner,
	}
	d.mu.Lock()
	d.conns[tc] = addr
	d.mu.Unlock()

	return tc, nil
}

func (d *dnsDialer) nextAddr() string {
	d.mu.Lock()
	addrs := append([]string(nil), d.addrs...)
	d.mu.Unlock()

	if len(addrs) == 0 {
		return d.addr
	}
	idx := d.next.Add(1) - 1
	return addrs[idx%uint64(len(addrs))]
}

func (d *dnsDialer) refreshLoop() {
	ticker := time.NewTicker(d.refreshInterval)
	defer ticker.Stop()

	for range ticker.C {
		d.refreshOnce()
	}
}

func (d *dnsDialer) refreshOnce() {
	addrs := d.resolve()
	if len(addrs) == 0 {
		return
	}

	d.mu.Lock()
	d.addrs = addrs
	toClose := d.rebalanceLocked()
	d.mu.Unlock()

	for _, conn := range toClose {
		_ = conn.Close()
	}
}

func (d *dnsDialer) resolve() []string {
	if d.host == "" || d.port == "" {
		return nil
	}
	if ip := net.ParseIP(d.host); ip != nil {
		return []string{net.JoinHostPort(ip.String(), d.port)}
	}

	ctx, cancel := context.WithTimeout(context.Background(), d.dialTimeout)
	defer cancel()

	ips, err := d.resolver.LookupIPAddr(ctx, d.host)
	if err != nil {
		return nil
	}

	seen := make(map[string]struct{}, len(ips))
	addrs := make([]string, 0, len(ips))
	for _, ip := range ips {
		if ip.IP == nil {
			continue
		}
		addr := net.JoinHostPort(ip.IP.String(), d.port)
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)
	return addrs
}

func (d *dnsDialer) rebalanceLocked() []*trackedConn {
	if len(d.addrs) == 0 || len(d.conns) == 0 {
		return nil
	}

	allowed := make(map[string]struct{}, len(d.addrs))
	counts := make(map[string]int, len(d.addrs))
	for _, addr := range d.addrs {
		allowed[addr] = struct{}{}
	}
	for _, addr := range d.conns {
		if _, ok := allowed[addr]; ok {
			counts[addr]++
		}
	}

	totalAllowed := 0
	for _, count := range counts {
		totalAllowed += count
	}
	target := 1
	if len(d.addrs) > 0 {
		target = totalAllowed / len(d.addrs)
		if target < 1 {
			target = 1
		}
	}

	var toClose []*trackedConn
	for conn, addr := range d.conns {
		_, isAllowed := allowed[addr]
		if (!isAllowed || counts[addr] > target) && conn.isIdle() {
			toClose = append(toClose, conn)
			if isAllowed {
				counts[addr]--
			}
		}
	}
	return toClose
}

func (d *dnsDialer) remove(conn *trackedConn) {
	d.mu.Lock()
	delete(d.conns, conn)
	d.mu.Unlock()
}

type trackedConn struct {
	net.Conn
	dialer *dnsDialer
	addr   string
	owner  *client
	once   sync.Once
}

func (c *trackedConn) isIdle() bool {
	return c.owner == nil || c.owner.c.PendingRequests() == 0
}

func (c *trackedConn) Close() error {
	var err error
	c.once.Do(func() {
		c.dialer.remove(c)
		err = c.Conn.Close()
	})
	return err
}
