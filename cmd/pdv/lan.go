package main

import (
	"log"
	"net"
	"strings"

	"github.com/grandcat/zeroconf"
)

// startLocalDiscovery publishes markyspdv.local through mDNS/DNS-SD.
// It advertises only usable IPv4 addresses; clients do not need a hosts-file entry.
func startLocalDiscovery(port int) *zeroconf.Server {
	var ips []string
	seen := map[string]bool{}
	interfaces, err := net.Interfaces()
	if err != nil {
		log.Printf("MARKYS PDV: não foi possível listar interfaces para mDNS: %v", err)
		return nil
	}
	var active []net.Interface
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		active = append(active, iface)
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch value := addr.(type) {
			case *net.IPNet:
				ip = value.IP
			case *net.IPAddr:
				ip = value.IP
			}
			ip = ip.To4()
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			value := ip.String()
			if !seen[value] {
				seen[value] = true
				ips = append(ips, value)
			}
		}
	}
	if len(ips) == 0 {
		log.Printf("MARKYS PDV: nenhum IPv4 de rede local encontrado; o acesso por IP ainda poderá funcionar.")
		return nil
	}

	server, err := zeroconf.RegisterProxy(
		"MARKYS PDV", "_http._tcp", "local.", port,
		"markyspdv.local.", ips,
		[]string{"path=/", "product=MARKYS-PDV"},
		active,
	)
	if err != nil {
		log.Printf("MARKYS PDV: anúncio automático markyspdv.local indisponível: %v", err)
		return nil
	}
	log.Printf("MARKYS PDV: endereço de rede anunciado por mDNS: http://markyspdv.local:%d", port)
	log.Printf("MARKYS PDV: IPv4 anunciado: %s", strings.Join(ips, ", "))
	return server
}
