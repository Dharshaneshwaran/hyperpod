package main

import "testing"

// These cases are the ones actually seen on the cluster, not invented ones.
func TestResolveRPCAddr(t *testing.T) {
	cases := []struct {
		name       string
		advertised string
		srcIP      string
		wantAddr   string
		wantReason bool // true when the controller must explain a substitution
	}{
		{
			// The multi-homed laptop: advertised its Wi-Fi address while the
			// broadcast arrived over Ethernet. Trust what reached us.
			name:       "advertised interface is not the one we heard from",
			advertised: "192.168.1.29:50052",
			srcIP:      "192.168.1.32",
			wantAddr:   "192.168.1.32:50052",
			wantReason: true,
		},
		{
			// An older worker build, or a launcher that no longer sets the
			// advertise variable: no address at all.
			name:       "no advertised address",
			advertised: "",
			srcIP:      "192.168.1.32",
			wantAddr:   "192.168.1.32:50052",
			wantReason: true,
		},
		{
			// Consistent: leave it exactly as the worker stated.
			name:       "advertised address matches the source",
			advertised: "192.168.1.5:50052",
			srcIP:      "192.168.1.5",
			wantAddr:   "192.168.1.5:50052",
			wantReason: false,
		},
		{
			// A non-default port must survive the host correction.
			name:       "custom port is preserved when the host is corrected",
			advertised: "192.168.1.29:51000",
			srcIP:      "192.168.1.32",
			wantAddr:   "192.168.1.32:51000",
			wantReason: true,
		},
		{
			name:       "unparseable address falls back to the source",
			advertised: "not-a-host-port",
			srcIP:      "192.168.1.32",
			wantAddr:   "192.168.1.32:50052",
			wantReason: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			addr, why := resolveRPCAddr(c.advertised, c.srcIP)
			if addr != c.wantAddr {
				t.Fatalf("addr = %q, want %q", addr, c.wantAddr)
			}
			if c.wantReason && why == "" {
				t.Fatal("a substitution must be explained, or it cannot be debugged")
			}
			if !c.wantReason && why != "" {
				t.Fatalf("no substitution was needed, but got reason %q", why)
			}
		})
	}
}
