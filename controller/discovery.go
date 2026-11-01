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
	"os"
	"time"
)

// DiscoveryPort is the UDP port workers broadcast to and the controller listens on.
const DiscoveryPort = 50999

// discoveryAnnounce is the payload a worker broadcasts.
type discoveryAnnounce struct {
	Type     string `json:"type"` // "hyperpod-worker"
	WorkerID string `json:"worker_id"`
	Address  string `json:"address"`  // http base, e.g. http://192.168.1.38:9100
	RPCAddr  string `json:"rpc_addr"` // host:port of this worker's ggml-rpc-server
	// RPCPort is the port the worker knows it configured. It is authoritative
	// where the host in RPCAddr may not be, on a machine with several
	// interfaces, so the controller pairs this port with the address it
	// observed the broadcast arriving from.
	RPCPort   string     `json:"rpc_port"`
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
		// Answer the worker with the address its own packet arrived on. A
		// worker otherwise has to be told where the controller is, and that
		// address changes whenever the machine moves network or switches
		// between Wi-Fi and Ethernet. Every such change previously meant
		// editing a launcher on every laptop, and a stale value shows up as a
		// connection timeout rather than anything self-explanatory.
		//
		// Replying costs one datagram and is sent to the source, so it reaches
		// the worker over the same path that already worked in one direction.
		s.replyWithControllerAddr(conn, src)

		// If the worker did not know its own IP, fall back to the UDP source IP.
		if a.Address == "" {
			a.Address = "http://" + src.IP.String() + ":9100"
		}

		// A worker on both Ethernet and Wi-Fi can advertise the interface the
		// controller cannot reach, and an older worker build may not advertise
		// one at all. The broadcast's source address is, by construction, one
		// that reaches us, so it is the safer choice in both cases.
		if addr, why := resolveRPCAddrWithPort(a.RPCAddr, a.RPCPort, src.IP.String()); why != "" {
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

// controllerHello is the reply to a worker's broadcast. It tells the worker
// where to send registrations and heartbeats, so that address never has to be
// configured by hand on each machine.
type controllerHello struct {
	Type       string `json:"type"`
	Controller string `json:"controller"`
}

// replyWithControllerAddr answers a worker using the local address its packet
// arrived on, which is by construction an address that reaches this controller
// from that worker.
func (s *server) replyWithControllerAddr(conn *net.UDPConn, src *net.UDPAddr) {
	local, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return
	}
	// A wildcard listener reports an unspecified IP, which is useless to send.
	// Ask the routing table which of our addresses reaches this worker.
	host := local.IP
	if host == nil || host.IsUnspecified() {
		probe, err := net.Dial("udp", src.String())
		if err != nil {
			return
		}
		defer probe.Close()
		if la, ok := probe.LocalAddr().(*net.UDPAddr); ok {
			host = la.IP
		}
	}
	if host == nil || host.IsUnspecified() {
		return
	}
	payload, err := json.Marshal(controllerHello{
		Type:       "hyperpod-controller",
		Controller: "http://" + net.JoinHostPort(host.String(), controllerPort()) + "",
	})
	if err != nil {
		return
	}
	_, _ = conn.WriteToUDP(payload, src)
}

// controllerPort is the port the HTTP API listens on, taken from the same
// setting that configures the listener so the two cannot disagree.
func controllerPort() string {
	addr := os.Getenv("HYPERPOD_CONTROLLER_ADDR")
	if addr == "" {
		return "8080"
	}
	if _, port, err := net.SplitHostPort(addr); err == nil && port != "" {
		return port
	}
	return "8080"
}

// resolveRPCAddrWithPort decides a worker's RPC endpoint.
//
// The port comes from the worker whenever it sends one: it knows which port it
// configured, and that cannot be wrong. The host comes from the address the
// broadcast actually arrived from, which the controller knows reaches it. Only
// when a worker sends neither does a default port get assumed, and that case is
// reported as an assumption rather than presented as fact.
func resolveRPCAddrWithPort(advertised, reportedPort, srcIP string) (addr, why string) {
	if reportedPort != "" {
		want := net.JoinHostPort(srcIP, reportedPort)
		if advertised == want {
			return advertised, ""
		}
		if advertised == "" {
			return want, fmt.Sprintf("advertised no rpc address; using %s from its broadcast source and its own port %s", want, reportedPort)
		}
		return want, fmt.Sprintf("advertised rpc=%s but broadcast came from %s; using %s with its own port", advertised, srcIP, want)
	}
	return resolveRPCAddr(advertised, srcIP)
}
