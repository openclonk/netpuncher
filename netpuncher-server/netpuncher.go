package main

import "github.com/lluchs/netpuncher/c4netioudp"
import "log"
import "net"

func main() {
	listenaddr := net.UDPAddr{IP: net.IPv6unspecified, Port: 11116}
	conn, err := net.ListenUDP("udp", &listenaddr)
	if err != nil {
		log.Fatal("couldn't ListenUDP", err)
	}
	log.Println("netpuncher listening on port", listenaddr.Port)

	buf := make([]byte, 1500)
	for {
		n, recvaddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Fatal("error while receiving", err)
		}
		hdr := c4netioudp.ReadPacketHdr(buf)
		log.Println("received", n, "byte from", recvaddr, "PacketHdr", hdr)
	}
}
