package main

import "fmt"
import "github.com/lluchs/netpuncher/c4netioudp"
import "net"
import "os"

func main() {
	raddr, err := net.ResolveUDPAddr("udp", os.Args[1])
	if err != nil {
		fmt.Errorf("invalid address", err)
		os.Exit(1)
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		fmt.Errorf("couldn't DialUDP", err)
		os.Exit(1)
	}
	fmt.Println("sending ping")
	ping := c4netioudp.PacketHdr{StatusByte: c4netioudp.IPID_Ping}
	n, err := ping.WriteTo(conn)
	if err != nil {
		fmt.Errorf("couldn't send UDP", err)
		os.Exit(1)
	}
	fmt.Println("sent", n, "byte")

	buf := make([]byte, 1500)
	nread, recvaddr, err := conn.ReadFromUDP(buf)
	if err != nil {
		fmt.Errorf("error while receiving", err)
		os.Exit(1)
	}
	hdr := c4netioudp.ReadPacketHdr(buf)
	if err != nil {
		fmt.Errorf("couldn't read PacketHdr", err)
		os.Exit(1)
	}
	fmt.Println("received", nread, "byte from", recvaddr, "PacketHdr", hdr)

	fmt.Println("sending ConnPacket")
	connpkg := c4netioudp.NewConnPacket(*raddr)
	n, err = connpkg.WriteTo(conn)
	if err != nil {
		fmt.Errorf("couldn't send UDP", err)
		os.Exit(1)
	}
	fmt.Println("sent", n, "byte")

	nread, recvaddr, err = conn.ReadFromUDP(buf)
	if err != nil {
		fmt.Errorf("error while receiving", err)
		os.Exit(1)
	}
	hdr = c4netioudp.ReadPacketHdr(buf)
	if err != nil {
		fmt.Errorf("couldn't read PacketHdr", err)
		os.Exit(1)
	}
	fmt.Println("received", nread, "byte from", recvaddr, "PacketHdr", hdr)
	if hdr.StatusByte == c4netioudp.IPID_Conn {
		connrepkg := c4netioudp.ReadConnPacket(buf)
		fmt.Println("Conn", connrepkg)

		connokpkg := c4netioudp.NewConnOkPacket(*recvaddr)
		n, err = connokpkg.WriteTo(conn)
		if err != nil {
			fmt.Errorf("couldn't send UDP", err)
			os.Exit(1)
		}
		fmt.Println("sent", n, "byte")
	}
}
