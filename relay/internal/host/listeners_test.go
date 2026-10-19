package host

import (
	"net/netip"
	"slices"
	"testing"
)

func TestParseListenersReadsEveryBoundAddress(t *testing.T) {
	output := "p82169\nf4\nn127.0.0.1:47123\nf5\nn[::1]:47123\np900\nf3\nn*:47123\nn127.0.0.1:147123\n"
	got := ParseListeners([]byte(output), 47123)
	want := []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.IPv6Loopback(), netip.IPv6Unspecified()}
	if !slices.Equal(got, want) {
		t.Fatalf("listeners = %v, want %v", got, want)
	}
}
