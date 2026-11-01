// HyperPod Controller — LAN auto-discovery (UDP broadcast listener).
//
// Workers periodically UDP-broadcast a small JSON announcement on DiscoveryPort.
// The controller listens for these and auto-registers/refreshes the worker in
// the same registry used by /status and the supervisor. No manual IPs needed:
// start a worker on the Wi-Fi and it appears in the dashboard.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"time"
)

// DiscoveryPort is the UDP port workers broadcast to and the controller listens on.
const DiscoveryPort = 50999

// discoveryAnnounce is the payload a worker broadcasts.
type discoveryAnnounce struct {
	Type      string     `json:"type"` // "hyperpod-worker"
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
			continue // ignore non-HyperPod traffic
		}
		if a.Type != "hyperpod-worker" || a.WorkerID == "" || !a.Telemetry.validVersion() {
			continue
		}
		// If the worker did not know its own IP, fall back to the UDP source IP.
		if a.Address == "" {
			a.Address = "http://" + src.IP.String() + ":9100"
		}

		// A worker on both Ethernet and Wi-Fi can advertise the interface the
		// controller cannot reach, and an older worker build may not advertise
		// one at all. The broadcast's source address is, by construction, one
		// that reaches us, so it is the safer choice in both cases.
		if addr, why := resolveRPCAddr(a.RPCAddr, src.IP.String()); why != "" {
			log.Printf("discovery: worker %q %s", a.WorkerID, why)
			a.RPCAddr = addr
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

// defaultRPCPort is the port ggml-rpc-server listens on in every launcher here.
// It is the assumption made when a worker advertises no RPC address at all.
const defaultRPCPort = "50052"

// resolveRPCAddr decides which RPC address to trust for a worker, given what it
// advertised and the source address its broadcast actually arrived from.
//
// It returns the address to use and, when that differs from what the worker
// claimed, a sentence explaining why. An empty reason means the advertised
// address was already consistent and must be left alone: the controller should
// not rewrite a worker's own account of itself without saying so.
func resolveRPCAddr(advertised, srcIP string) (addr, why string) {
	if advertised == "" {
		a := net.JoinHostPort(srcIP, defaultRPCPort)
		return a, fmt.Sprintf("advertised no rpc address; assuming %s from its broadcast source", a)
	}
	host, port, err := net.SplitHostPort(advertised)
	if err != nil {
		a := net.JoinHostPort(srcIP, defaultRPCPort)
		return a, fmt.Sprintf("advertised an unparseable rpc address %q; assuming %s", advertised, a)
	}
	if host == srcIP {
		return advertised, ""
	}
	a := net.JoinHostPort(srcIP, port)
	return a, fmt.Sprintf("advertised rpc=%s but broadcast came from %s; using %s", advertised, srcIP, a)
}
