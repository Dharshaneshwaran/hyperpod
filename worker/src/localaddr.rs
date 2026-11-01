//! Which of this machine's addresses the controller can actually reach.
//!
//! A laptop plugged into Ethernet while still joined to Wi-Fi has two usable
//! addresses, and only one of them is on the path to the controller. Picking
//! the wrong one produces a worker that heartbeats normally -- heartbeats go
//! outbound -- while every inbound RPC connection fails, because the address it
//! advertised is not reachable from where the controller sits.
//!
//! The fix is to let the operating system answer the question. Connecting a UDP
//! socket to the controller's address sends nothing, but it does make the kernel
//! consult its routing table and bind a source address. That source address is,
//! by definition, the one the controller will see, so it is the one to
//! advertise. It also follows the route automatically: plug in Ethernet and the
//! answer changes with it.

use std::net::{IpAddr, SocketAddr, ToSocketAddrs, UdpSocket};

/// Split a controller URL such as `http://192.168.1.34:8080` into host and port.
fn host_port(url: &str) -> Option<(String, u16)> {
    let rest = url
        .strip_prefix("http://")
        .or_else(|| url.strip_prefix("https://"))
        .unwrap_or(url);
    let rest = rest.split('/').next()?;
    // Bracketed IPv6, e.g. [::1]:8080. Index the original string, not the
    // stripped one, or the closing bracket lands one byte early.
    if rest.starts_with('[') {
        let close = rest.find(']')?;
        let host = &rest[1..close];
        let port = rest[close + 1..]
            .strip_prefix(':')
            .and_then(|p| p.parse().ok());
        return Some((host.to_string(), port.unwrap_or(8080)));
    }
    let mut parts = rest.rsplitn(2, ':');
    match (parts.next(), parts.next()) {
        (Some(port), Some(host)) if !host.is_empty() => {
            Some((host.to_string(), port.parse().unwrap_or(8080)))
        }
        _ => Some((rest.to_string(), 8080)),
    }
}

/// The local address the kernel would use to reach `controller_url`.
///
/// Returns `None` when the host cannot be resolved or no route exists, so the
/// caller can fall back rather than advertise something invented.
pub fn toward(controller_url: &str) -> Option<IpAddr> {
    let (host, port) = host_port(controller_url)?;
    // Resolve first: the controller may be given by name.
    let target: SocketAddr = (host.as_str(), port).to_socket_addrs().ok()?.next()?;
    let bind = if target.is_ipv4() {
        "0.0.0.0:0"
    } else {
        "[::]:0"
    };
    let sock = UdpSocket::bind(bind).ok()?;
    // Connect on a datagram socket sends no packets; it only fixes the route.
    sock.connect(target).ok()?;
    let local = sock.local_addr().ok()?.ip();
    if local.is_unspecified() || local.is_loopback() && !target.ip().is_loopback() {
        return None;
    }
    Some(local)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_controller_urls() {
        assert_eq!(
            host_port("http://192.168.1.34:8080"),
            Some(("192.168.1.34".to_string(), 8080))
        );
        assert_eq!(
            host_port("192.168.1.34:9999"),
            Some(("192.168.1.34".to_string(), 9999))
        );
        // A missing port falls back to the controller's default.
        assert_eq!(
            host_port("http://controller.local"),
            Some(("controller.local".to_string(), 8080))
        );
        assert_eq!(
            host_port("http://[::1]:8080"),
            Some(("::1".to_string(), 8080))
        );
    }

    #[test]
    fn finds_the_loopback_route_to_itself() {
        // Routing to loopback must yield a loopback source, which proves the
        // socket trick reports a real routing decision.
        let ip = toward("http://127.0.0.1:8080").expect("a route to loopback must exist");
        assert!(ip.is_loopback(), "expected loopback, got {ip}");
    }

    #[test]
    fn unresolvable_host_yields_none() {
        assert!(toward("http://this-host-does-not-exist.invalid:8080").is_none());
    }
}
