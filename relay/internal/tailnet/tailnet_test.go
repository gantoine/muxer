package tailnet

import (
	"errors"
	"net/netip"
	"testing"
)

const serveStatusFixture = `{
  "TCP": {"47123": {"HTTPS": true}, "60001": {"HTTPS": true}, "2222": {"TCPForward": "127.0.0.1:22"}},
  "Web": {
    "mac.tailnet.ts.net:47123": {"Handlers": {"/": {"Proxy": "http://127.0.0.1:47123"}}},
    "mac.tailnet.ts.net:60001": {"Handlers": {"/": {"Proxy": "http://127.0.0.1:8765"}, "/docs": {"Path": "/srv"}}}
  }
}`

func TestServeConfigFindsSingleProxyHandlers(t *testing.T) {
	config, err := ParseServeConfig([]byte(serveStatusFixture))
	if err != nil {
		t.Fatal(err)
	}
	if proxy := config.HTTPSProxy("mac.tailnet.ts.net", 47123); proxy != "http://127.0.0.1:47123" {
		t.Fatalf("proxy = %q", proxy)
	}
	if proxy := config.HTTPSProxy("mac.tailnet.ts.net", 60001); proxy != "" {
		t.Fatalf("an entry with several handlers is not a plain proxy: %q", proxy)
	}
	if !config.Uses(2222) || config.HTTPSProxy("mac.tailnet.ts.net", 2222) != "" || config.Uses(3000) {
		t.Fatal("TCP forwards use their port but are not HTTPS proxies")
	}
	if empty, err := ParseServeConfig([]byte("\n")); err != nil || empty.Uses(47123) {
		t.Fatalf("an empty serve status means nothing is served: %v", err)
	}
}

func TestStatusNeedsARunningLoggedInNode(t *testing.T) {
	self, err := ParseStatus([]byte(`{"BackendState":"Running","Self":{"DNSName":"mac.tailnet.ts.net.","TailscaleIPs":["100.64.0.7","fd7a:115c:a1e0::7"]}}`))
	if err != nil || self.DNSName != "mac.tailnet.ts.net" || !self.Has(netip.MustParseAddr("100.64.0.7")) || self.Has(netip.MustParseAddr("127.0.0.1")) {
		t.Fatalf("self = %+v, %v", self, err)
	}
	if _, err := ParseStatus([]byte(`{"BackendState":"NeedsLogin","Self":{"DNSName":""}}`)); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("a logged out node must be reported: %v", err)
	}
}
