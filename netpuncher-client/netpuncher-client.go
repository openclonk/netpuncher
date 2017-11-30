package main

import "fmt"
import "github.com/lluchs/netpuncher/c4netioudp"
import "net"
import "os"

func main() {
	raddr, err := net.ResolveUDPAddr("udp", os.Args[1])
	if err != nil {
		fmt.Println("invalid address", err)
		os.Exit(1)
	}
	conn, err := c4netioudp.Dial("udp", nil, raddr)
	if err != nil {
		fmt.Println("couldn't Dial", err)
		os.Exit(1)
	}
	defer conn.Close()
	//fmt.Println("sending ping")
	//ping := c4netioudp.PacketHdr{StatusByte: c4netioudp.IPID_Ping}
	//n, err := ping.WriteTo(conn)
	//if err != nil {
	//	fmt.Println("couldn't send UDP", err)
	//	os.Exit(1)
	//}
	//fmt.Println("sent", n, "byte")

	buf := make([]byte, 1500)
	nread, err := conn.Read(buf)
	if err != nil {
		fmt.Println("error while reading", err)
		os.Exit(1)
	}
	fmt.Println("received", nread, "byte")
	fmt.Printf("%x\n", buf[:nread])
}
