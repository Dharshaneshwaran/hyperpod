// HyperPot Controller — LAN auto-discovery (UDP broadcast listener).
//
// Workers periodically UDP-broadcast a small JSON announcement on DiscoveryPort.
// The controller listens for these and auto-registers/refreshes the worker in
// the same registry used by /status and the supervisor. No manual IPs needed:
// start a worker on the Wi-Fi and it appears in the dashboard.
package main

import (
	"encoding/json"
	"log"
	"net"
	"time"
)

// DiscoveryPort is the UDP port workers broadcast to and the controller listens on.
const DiscoveryPort = 50999

// discoveryAnnounce is the payload a worker broadcasts.
type discoveryAnnounce struct {
	Type      string     `json:"type"` // "hyperpot-worker"
	WorkerID  string     `json:"worker_id"`
	Address   string     `json:"address"`  // http base, e.g. http://192.168.1.38:9100
	RPCAddr   string     `json:"rpc_addr"` // host:port of this worker's ggml-rpc-server
	GPU       string     `json:"gpu"`
	OS        string     `json:"os"`
	Telemetry *Telemetry `json:"telemetry"`
}

// startDiscovery listens for worker broadcasts and registers them.
func (s *server) startDiscovery() {
	addr := &net.UDPAddr{IP: net.IPv4zero, Port: DiscoveryPort}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		log.Printf("discovery: could not listen on udp :%d: %v (auto-discovery disabled)", DiscoveryPort, err)
		return
	}
	log.Printf("discovery: listening for worker broadcasts on udp :%d", DiscoveryPort)

	buf := make([]byte, 2048)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("discovery: read error: %v", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		var a discoveryAnnounce
		if err := json.Unmarshal(buf[:n], &a); err != nil {
			continue // ignore non-HyperPot traffic
		}
		if a.Type != "hyperpot-worker" || a.WorkerID == "" || !a.Telemetry.validVersion() {
			continue
		}
		// If the worker did not know its own IP, fall back to the UDP source IP.
		if a.Address == "" {
			a.Address = "http://" + src.IP.String() + ":9100"
		}

		gpu := a.GPU
		if a.Telemetry != nil && a.Telemetry.GPU != nil && a.Telemetry.GPU.Name != nil {
			gpu = *a.Telemetry.GPU.Name
		}
		known := s.reg.isKnown(a.WorkerID)
		s.reg.register(&worker{
			ID:        a.WorkerID,
			Address:   a.Address,
			RPCAddr:   a.RPCAddr,
			GPU:       gpu,
			OS:        a.OS,
			Telemetry: a.Telemetry,
		})
		if !known {
			log.Printf("discovery: auto-registered worker %q from %s (rpc=%s)", a.WorkerID, src.IP, a.RPCAddr)
		}
	}
}
