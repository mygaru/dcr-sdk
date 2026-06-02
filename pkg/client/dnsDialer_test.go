package client

import (
	"context"
	"net"
	"testing"
	"time"
)

type fakeDNSResolver struct {
	ips []net.IPAddr
}

func (r *fakeDNSResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return r.ips, nil
}

func TestDNSDialerInitialResolveAndRoundRobin(t *testing.T) {
	resolver := &fakeDNSResolver{
		ips: []net.IPAddr{
			{IP: net.ParseIP("178.63.252.112")},
			{IP: net.ParseIP("178.63.252.110")},
			{IP: net.ParseIP("178.63.252.111")},
		},
	}

	dialer := newDNSDialerWithResolver("cloud.mygaru.com:7943", time.Second, -1, resolver)

	want := []string{
		"178.63.252.110:7943",
		"178.63.252.111:7943",
		"178.63.252.112:7943",
		"178.63.252.110:7943",
		"178.63.252.111:7943",
		"178.63.252.112:7943",
		"178.63.252.110:7943",
	}
	for i, addr := range want {
		if got := dialer.nextAddr(); got != addr {
			t.Fatalf("addr[%d]: expected %q, got %q", i, addr, got)
		}
	}
}

func TestDNSDialerRefreshUpdatesResolvedAddrs(t *testing.T) {
	resolver := &fakeDNSResolver{
		ips: []net.IPAddr{
			{IP: net.ParseIP("178.63.252.110")},
		},
	}
	dialer := newDNSDialerWithResolver("cloud.mygaru.com:7943", time.Second, -1, resolver)

	resolver.ips = []net.IPAddr{
		{IP: net.ParseIP("178.63.252.111")},
		{IP: net.ParseIP("178.63.252.112")},
	}
	dialer.refreshOnce()

	want := []string{
		"178.63.252.111:7943",
		"178.63.252.112:7943",
	}
	for i, addr := range want {
		if got := dialer.nextAddr(); got != addr {
			t.Fatalf("addr[%d]: expected %q, got %q", i, addr, got)
		}
	}
}

func TestDNSDialerRebalanceClosesIdleOverrepresentedConns(t *testing.T) {
	dialer := newDNSDialerWithResolver("cloud.mygaru.com:7943", time.Second, -1, &fakeDNSResolver{
		ips: []net.IPAddr{
			{IP: net.ParseIP("178.63.252.110")},
			{IP: net.ParseIP("178.63.252.111")},
		},
	})

	connA1 := newTestTrackedConn(t, dialer, "178.63.252.110:7943")
	connA2 := newTestTrackedConn(t, dialer, "178.63.252.110:7943")
	connB := newTestTrackedConn(t, dialer, "178.63.252.111:7943")

	dialer.mu.Lock()
	dialer.conns[connA1] = connA1.addr
	dialer.conns[connA2] = connA2.addr
	dialer.conns[connB] = connB.addr
	toClose := dialer.rebalanceLocked()
	dialer.mu.Unlock()

	if len(toClose) != 1 {
		t.Fatalf("expected one overrepresented connection to close, got %d", len(toClose))
	}
	if toClose[0].addr != "178.63.252.110:7943" {
		t.Fatalf("expected overrepresented 178.63.252.110 connection to close, got %q", toClose[0].addr)
	}
}

func TestDNSDialerRebalanceClosesConnsForRemovedAddrs(t *testing.T) {
	dialer := newDNSDialerWithResolver("cloud.mygaru.com:7943", time.Second, -1, &fakeDNSResolver{
		ips: []net.IPAddr{
			{IP: net.ParseIP("178.63.252.110")},
		},
	})
	dialer.addrs = []string{"178.63.252.111:7943"}

	conn := newTestTrackedConn(t, dialer, "178.63.252.110:7943")
	dialer.mu.Lock()
	dialer.conns[conn] = conn.addr
	toClose := dialer.rebalanceLocked()
	dialer.mu.Unlock()

	if len(toClose) != 1 {
		t.Fatalf("expected removed address connection to close, got %d", len(toClose))
	}
	if toClose[0] != conn {
		t.Fatalf("expected removed address connection to close")
	}
}

func newTestTrackedConn(t *testing.T, dialer *dnsDialer, addr string) *trackedConn {
	t.Helper()

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	return &trackedConn{
		Conn:   clientConn,
		dialer: dialer,
		addr:   addr,
	}
}
