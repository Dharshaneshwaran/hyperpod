package main

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
)

// The worker knows which port it bound; the controller knows which address
// reached it. Pairing those two is the only combination that needs no guess.
func TestResolveRPCAddrWithPortNeverAssumesTheDefault(t *testing.T) {
	const src = "192.168.1.29"

	cases := []struct {
		name         string
		advertised   string
		reportedPort string
		wantAddr     string
		wantReason   bool
	}{
		{
			// The case that mattered: a worker on a non-default port that could
			// not name its own host. Assuming 50052 would have been wrong.
			name:         "custom port is honoured when no address is advertised",
			advertised:   "",
			reportedPort: "51234",
			wantAddr:     "192.168.1.29:51234",
			wantReason:   true,
		},
		{
			name:         "host corrected but the worker's own port is kept",
			advertised:   "10.1.2.3:51234",
			reportedPort: "51234",
			wantAddr:     "192.168.1.29:51234",
			wantReason:   true,
		},
		{
			name:         "already consistent, left untouched",
			advertised:   "192.168.1.29:51234",
			reportedPort: "51234",
			wantAddr:     "192.168.1.29:51234",
			wantReason:   false,
		},
		{
			// Only an older worker that reports no port at all falls back, and
			// the reason must say it is an assumption.
			name:         "no port reported falls back and says so",
			advertised:   "",
			reportedPort: "",
			wantAddr:     "192.168.1.29:" + defaultRPCPort,
			wantReason:   true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			addr, why := resolveRPCAddrWithPort(c.advertised, c.reportedPort, src)
			if addr != c.wantAddr {
				t.Fatalf("addr = %q, want %q", addr, c.wantAddr)
			}
			if c.wantReason != (why != "") {
				t.Fatalf("reason = %q, wantReason = %v", why, c.wantReason)
			}
			// A fallback must be identifiable as an assumption, not stated as fact.
			if c.reportedPort == "" && why != "" && !strings.Contains(why, "assuming") {
				t.Fatalf("a default-port fallback must be labelled an assumption, got %q", why)
			}
			// When the worker reported a port, that port must appear verbatim.
			if c.reportedPort != "" && !strings.HasSuffix(addr, ":"+c.reportedPort) {
				t.Fatalf("addr %q does not end in the worker's reported port %q", addr, c.reportedPort)
			}
		})
	}
}

// The reply tells a worker where to reach the controller, so it must be a
// usable URL and must carry the type a worker checks for.
func TestControllerHelloIsAUsableURL(t *testing.T) {
	hello := controllerHello{
		Type:       "hyperpod-controller",
		Controller: "http://" + net.JoinHostPort("192.168.1.20", "8080"),
	}
	raw, err := json.Marshal(hello)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back controllerHello
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Type != "hyperpod-controller" {
		t.Fatalf("type = %q; a worker filters on this", back.Type)
	}
	if back.Controller != "http://192.168.1.20:8080" {
		t.Fatalf("controller = %q, want http://192.168.1.20:8080", back.Controller)
	}
}

// The advertised port must come from the same setting as the listener, or the
// reply would send workers to a port nothing is listening on.
func TestControllerPortFollowsTheListenerSetting(t *testing.T) {
	t.Setenv("HYPERPOD_CONTROLLER_ADDR", "")
	if got := controllerPort(); got != "8080" {
		t.Fatalf("default port = %q, want 8080", got)
	}
	t.Setenv("HYPERPOD_CONTROLLER_ADDR", ":9999")
	if got := controllerPort(); got != "9999" {
		t.Fatalf("port = %q, want 9999", got)
	}
	t.Setenv("HYPERPOD_CONTROLLER_ADDR", "127.0.0.1:7070")
	if got := controllerPort(); got != "7070" {
		t.Fatalf("port = %q, want 7070", got)
	}
}
