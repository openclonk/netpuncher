package main

import "fmt"
import "github.com/lluchs/netpuncher"
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

	msg, err := netpuncher.ReadFrom(conn)
	if err != nil {
		fmt.Println("error while reading", err)
		os.Exit(1)
	}
	fmt.Printf("got message type 0x%x %T: %+v\n", msg.Type(), msg, msg)
}
