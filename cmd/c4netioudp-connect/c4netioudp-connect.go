package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/openclonk/netpuncher/c4netioudp"

	"github.com/apex/log"
	"github.com/apex/log/handlers/cli"
)

const (
	punchTimeout  = 5 * time.Second
	punchInterval = 50 * time.Millisecond
)

var port = flag.Int("port", 0, "local port to use (default: random)")
var v4 = flag.Bool("4", false, "use IPv4")
var v6 = flag.Bool("6", false, "use IPv6")
var verbose = flag.Bool("v", false, "more log output")

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <connect address>\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	log.SetHandler(cli.Default)
	if *verbose {
		log.SetLevel(log.DebugLevel)
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
		log.WithError(err).Fatal("invalid address")
	}
	ip := net.IPv6unspecified
	if *v4 {
		ip = net.IPv4zero
	}
	laddr := net.UDPAddr{IP: ip, Port: *port}
	listener, err := c4netioudp.Listen(network, &laddr)
	if err != nil {
		log.WithError(err).Fatal("c4netioudp Listen failed")
	}
	defer listener.Close()

	conn, err := listener.Dial(raddr)
	if err != nil {
		log.WithError(err).Fatal("c4netioudp Dial failed")
	}
	defer conn.Close()

	fmt.Println("connected successfully!")
}

