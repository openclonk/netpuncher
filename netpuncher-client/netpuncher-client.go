package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/lluchs/netpuncher"
	"github.com/lluchs/netpuncher/c4netioudp"
)

var host = flag.Bool("host", false, "simulate host behavior")
var client = flag.Int("client", -1, "simulate client joining a host with given id")
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
	conn, err := c4netioudp.Dial(network, nil, raddr)
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
	switch np := msg.(type) {
	case *netpuncher.AssID:
		fmt.Printf("CID = %d\n", np.CID)
	default:
		fmt.Printf("got unexpected message type 0x%x %T: %+v\n", msg.Type(), msg, msg)
	}

	if *client >= 0 {
		// Request punching for the given host id.
		sreq := netpuncher.SReq{uint32(*client)}
		b, _ := sreq.MarshalBinary()
		conn.Write(b)
		fmt.Printf("-> %T: %+v\n", sreq, sreq)
		time.Sleep(1 * time.Second)
	}

	if *host {
		go func() {
			for {
				msg, err := netpuncher.ReadFrom(conn)
				if err != nil {
					fmt.Println("error while reading", err)
					os.Exit(1)
				}
				fmt.Printf("<- %T: %+v\n", msg, msg)
				// TODO: Respond to CReq
			}
		}()
		// Wait for an interrupt. Without this special handling, the connection
		// would not be closed properly.
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
	}
}
