package agent

import (
	"net"
	"strings"

	nodev1 "github.com/vyto4ka/vpn/internal/proto/vpn/node/v1"
)

// virtualPrefixes are interfaces whose addresses are never useful for inbounds.
var virtualPrefixes = []string{"docker", "br-", "veth", "virbr", "cni", "flannel", "cali", "lxc", "tun", "tap"}

// LocalAddresses lists global addresses of physical interfaces; the one used for the default
// route is marked primary (docs/INBOUNDS.md §2.2).
func LocalAddresses() []*nodev1.Address {
	primary := primaryIP()
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []*nodev1.Address
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || isVirtual(ifc.Name) {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.IsLoopback() || ipnet.IP.IsLinkLocalUnicast() || !ipnet.IP.IsGlobalUnicast() {
				continue
			}
			out = append(out, &nodev1.Address{Ip: ipnet.IP.String(), Interface: ifc.Name, Primary: primary != nil && ipnet.IP.Equal(primary)})
		}
	}
	return out
}

func isVirtual(name string) bool {
	for _, p := range virtualPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// primaryIP is the source address of the default route (no packets are sent).
func primaryIP() net.IP {
	c, err := net.Dial("udp", "1.1.1.1:53")
	if err != nil {
		return nil
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP
}
