// Package netinfo finds the phone's LAN addresses for `hampp share`.
package netinfo

import (
	"net"
	"sort"
)

// LANIPs returns private IPv4 addresses of this device.
//
// net.InterfaceAddrs uses netlink, which Android blocks for apps targeting
// SDK >= 30 (golang/go#40569). Official Termux targets SDK 28, but in case it
// fails we fall back to the address the kernel would use for an outbound UDP
// "connection" – that needs no netlink and sends no packets.
func LANIPs() []string {
	var ips []string
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				if ip := n.IP.To4(); ip != nil && ip.IsPrivate() {
					ips = append(ips, ip.String())
				}
			}
		}
	}
	if len(ips) == 0 {
		if ip := outboundIP(); ip != "" {
			ips = append(ips, ip)
		}
	}
	sort.Strings(ips)
	return ips
}

func outboundIP() string {
	c, err := net.Dial("udp4", "192.0.2.1:9") // TEST-NET-1; nothing is sent
	if err != nil {
		return ""
	}
	defer c.Close()
	ip := c.LocalAddr().(*net.UDPAddr).IP.To4()
	if ip == nil || !ip.IsPrivate() {
		return ""
	}
	return ip.String()
}
