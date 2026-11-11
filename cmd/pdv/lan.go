package main

import (
	"log"
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/net/ipv4"
)

const (
	markysMDNSName = "markyspdv.local."
	markysMDNSPort = 5353
	markysMDNSIPv4 = "224.0.0.251"
)

// startLocalDiscovery answers mDNS A-record queries for markyspdv.local.
// It runs inside the server, so client computers do not need hosts-file changes.
func startLocalDiscovery(port int) {
	interfaces, err := net.Interfaces()
	if err != nil {
		log.Printf("MARKYS PDV: nao foi possivel listar interfaces mDNS: %v", err)
		return
	}

	group := &net.UDPAddr{IP: net.ParseIP(markysMDNSIPv4), Port: markysMDNSPort}
	started := 0
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		var ips []net.IP
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
			ips = append(ips, ip)
		}
		if len(ips) == 0 {
			continue
		}

		conn, err := net.ListenMulticastUDP("udp4", &iface, group)
		if err != nil {
			log.Printf("MARKYS PDV: mDNS indisponivel na interface %s: %v", iface.Name, err)
			continue
		}
		packetConn := ipv4.NewPacketConn(conn)
		_ = packetConn.SetMulticastTTL(255)
		_ = packetConn.SetMulticastInterface(&iface)
		started++
		go serveMarkysMDNS(conn, packetConn, group, ips, port, iface.Name)
	}
	if started == 0 {
		log.Printf("MARKYS PDV: nenhuma interface IPv4 multicast disponivel; use o IP local do servidor.")
		return
	}
	log.Printf("MARKYS PDV: anunciando %s por mDNS na porta %d (%d interface(s))", strings.TrimSuffix(markysMDNSName, "."), port, started)
}

func serveMarkysMDNS(conn *net.UDPConn, packetConn *ipv4.PacketConn, group *net.UDPAddr, ips []net.IP, port int, interfaceName string) {
	defer conn.Close()
	buffer := make([]byte, 9000)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Minute))
		n, source, err := conn.ReadFromUDP(buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return
		}
		var query dns.Msg
		if err := query.Unpack(buffer[:n]); err != nil || query.Response {
			continue
		}

		response := new(dns.Msg)
		response.SetReply(&query)
		response.Authoritative = true
		response.RecursionDesired = false
		response.Compress = true
		for _, question := range query.Question {
			if !strings.EqualFold(question.Name, markysMDNSName) || (question.Qtype != dns.TypeA && question.Qtype != dns.TypeANY) || question.Qclass&0x7fff != dns.ClassINET {
				continue
			}
			for _, ip := range ips {
				response.Answer = append(response.Answer, &dns.A{
					Hdr: dns.RR_Header{Name: markysMDNSName, Rrtype: dns.TypeA, Class: dns.ClassINET | 0x8000, Ttl: 120},
					A: ip,
				})
			}
		}
		if len(response.Answer) == 0 {
			continue
		}
		packet, err := response.Pack()
		if err != nil {
			continue
		}

		destination := group
		for _, question := range query.Question {
			if strings.EqualFold(question.Name, markysMDNSName) && question.Qclass&0x8000 != 0 {
				destination = source
				break
			}
		}
		if destination == group {
			_, _ = packetConn.WriteTo(packet, nil, group)
		} else {
			_, _ = conn.WriteToUDP(packet, destination)
		}
	}
}
