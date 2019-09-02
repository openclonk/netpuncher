package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/openclonk/netpuncher"
	"github.com/openclonk/netpuncher/c4netioudp"
)

var host = flag.Bool("host", false, "simulate host behavior")
var client = flag.Int("client", -1, "simulate client joining a host with given id")
var port = flag.Int("port", 0, "local port to use (default: random)")
var v4 = flag.Bool("4", false, "use IPv4")
var v6 = flag.Bool("6", false, "use IPv6")

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <netpuncher address>\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	network := "udp"
	if *v4 {
		network = "udp4"
	}
	if *v6 {
		network = "udp6"
	}
	raddr, err := net.ResolveUDPAddr(network, flag.Arg(0))
	if err != nil {
		fmt.Println("invalid address", err)
		os.Exit(1)
	}
	laddr := net.UDPAddr{IP: net.IPv6unspecified, Port: *port}
	listener, err := c4netioudp.Listen(network, &laddr)
	if err != nil {
		fmt.Println("couldn't Listen: ", err)
		os.Exit(1)
	}
	defer listener.Close()

	conn, err := listener.Dial(raddr)
	if err != nil {
		fmt.Println("couldn't Dial: ", err)
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

	// The following uses version 1 of the netpuncher protocol.
	header := netpuncher.Header{Version: 1}

	if *client >= 0 {
		// Request punching for the given host id.
		sreq := netpuncher.SReq{Header: header, CID: uint32(*client)}
		b, err := sreq.MarshalBinary()
		if err != nil {
			panic(err)
		}
		conn.Write(b)
		fmt.Printf("-> %T: %+v\n", sreq, sreq)
		go handleMessages(conn)
		time.Sleep(1 * time.Second)
	}

	if *host {
		// Request an ID.
		b, err := netpuncher.IDReq{Header: header}.MarshalBinary()
		if err != nil {
			panic(err)
		}
		conn.Write(b)
		go handleMessages(conn)
		// Wait for an interrupt. Without this special handling, the connection
		// would not be closed properly.
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
	}
}

// Handle and print incoming messages.
func handleMessages(conn *c4netioudp.Conn) {
	for {
		msg, err := netpuncher.ReadFrom(conn)
		if err != nil {
			fmt.Println("error while reading:", err)
			os.Exit(1)
		}
		switch np := msg.(type) {
		case *netpuncher.AssID:
			fmt.Printf("CID = %d\n", np.CID)
		case *netpuncher.CReq:
			fmt.Printf("<- %T: %+v\n", msg, msg)
			// Send ping to the other side.
			if err = conn.SendTest(&np.Addr); err != nil {
				fmt.Println("couldn't send test:", err)
			}
		default:
			fmt.Printf("<- %T: %+v\n", msg, msg)
		}
	}
}
