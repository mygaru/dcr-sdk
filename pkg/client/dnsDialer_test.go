package client

import (
	"context"
	"net"
	"reflect"
	"strings"
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

// A name with both A and AAAA records must rotate over the IPv4 addresses only.
// The v6 ones are not merely useless here: dial passes them to
// fasthttp.DialTimeout, which resolves over tcp4, finds nothing to connect to and
// reports a DNS failure for a name that resolved fine.
func TestDNSDialerSkipsIPv6(t *testing.T) {
	resolver := &fakeDNSResolver{
		ips: []net.IPAddr{
			{IP: net.ParseIP("2a01:4f8:2b03:11ac::2")},
			{IP: net.ParseIP("23.88.24.90")},
			{IP: net.ParseIP("2a01:4f8:2b02:256::2")},
			{IP: net.ParseIP("46.4.208.61")},
			{IP: net.ParseIP("2a01:4f8:2b03:140c::2")},
			{IP: net.ParseIP("178.63.252.110")},
		},
	}

	dialer := newDNSDialerWithResolver("cloud.mygaru.com:7937", time.Second, -1, resolver)

	want := []string{
		"178.63.252.110:7937",
		"23.88.24.90:7937",
		"46.4.208.61:7937",
	}

	if !reflect.DeepEqual(dialer.addrs, want) {
		t.Fatalf("rotation = %v, want only the IPv4 addresses %v", dialer.addrs, want)
	}

	// And the rotation keeps returning those, rather than cycling into a v6 entry.
	for i := 0; i < len(want)*2; i++ {
		got := dialer.nextAddr()
		if strings.Contains(got, "[") {
			t.Fatalf("nextAddr() returned an IPv6 address %q", got)
		}
	}
}

// A name that resolves to IPv6 only has nothing this dialer can use. It must fall
// back to the name itself rather than silently rotating over unusable addresses:
// the refresh loop can still recover if an A record appears later.
func TestDNSDialerIPv6OnlyFallsBackToTheName(t *testing.T) {
	resolver := &fakeDNSResolver{
		ips: []net.IPAddr{
			{IP: net.ParseIP("2a01:4f8:2b03:11ac::2")},
			{IP: net.ParseIP("2a01:4f8:2b02:256::2")},
		},
	}

	dialer := newDNSDialerWithResolver("cloud.mygaru.com:7937", time.Second, -1, resolver)

	if want := []string{"cloud.mygaru.com:7937"}; !reflect.DeepEqual(dialer.addrs, want) {
		t.Errorf("rotation = %v, want the unresolved name %v", dialer.addrs, want)
	}
}

// Refreshing onto an IPv6-only answer must not empty the rotation: keeping the
// last usable addresses beats having none.
func TestDNSDialerRefreshIgnoresIPv6OnlyAnswer(t *testing.T) {
	resolver := &fakeDNSResolver{
		ips: []net.IPAddr{{IP: net.ParseIP("23.88.24.90")}},
	}

	dialer := newDNSDialerWithResolver("cloud.mygaru.com:7937", time.Second, -1, resolver)

	resolver.ips = []net.IPAddr{{IP: net.ParseIP("2a01:4f8:2b03:11ac::2")}}
	dialer.refreshOnce()

	if want := []string{"23.88.24.90:7937"}; !reflect.DeepEqual(dialer.addrs, want) {
		t.Errorf("rotation = %v, want the previous IPv4 address kept %v", dialer.addrs, want)
	}
}
