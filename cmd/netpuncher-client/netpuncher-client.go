package main

import (
	"bufio"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/openclonk/netpuncher"
	"github.com/openclonk/netpuncher/c4netioudp"

	"github.com/apex/log"
	"github.com/apex/log/handlers/cli"
)

const (
	punchTimeout  = 5 * time.Second
	punchInterval = 50 * time.Millisecond
)

var host = flag.Bool("host", false, "simulate host behavior")
var client = flag.Int("client", -1, "simulate client joining a host with given id")
var port = flag.Int("port", 0, "local port to use (default: random)")
var v4 = flag.Bool("4", false, "use IPv4")
var v6 = flag.Bool("6", false, "use IPv6")
var verbose = flag.Bool("v", false, "more log output")

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
		fmt.Println("invalid address", err)
		os.Exit(1)
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

	// The following uses version 1 of the netpuncher protocol.
	header := netpuncher.Header{Version: 1}

	if *client >= 0 {
		// Request punching for the given host id.
		sreq := netpuncher.SReq{Header: header, CID: uint32(*client)}
		b, err := sreq.MarshalBinary()
		if err != nil {
			log.WithError(err).Fatal("SReq.MarshalBinary failed")
		}
		conn.Write(b)
		log.WithField("packet", fmt.Sprintf("%+v", sreq)).Infof("-> %T", sreq)
		go handleMessages(listener, conn, false)
		if *v6 {
			// IPv6 => also request TCP punching
			sreqtcp := netpuncher.SReqTCP{Header: header, CID: uint32(*client)}
			b, err = sreqtcp.MarshalBinary()
			if err != nil {
				log.WithError(err).Fatal("SReqTCP.MarshalBinary failed")
			}
			conn.Write(b)
			log.WithField("packet", fmt.Sprintf("%+v", sreqtcp)).Infof("-> %T", sreqtcp)
		}
		time.Sleep(10 * time.Second)
	}

	if *host {
		// Request an ID.
		b, err := netpuncher.IDReq{Header: header}.MarshalBinary()
		if err != nil {
			panic(err)
		}
		conn.Write(b)
		go handleMessages(listener, conn, true)
		go handleConn(listener)
		// Wait for an interrupt. Without this special handling, the connection
		// would not be closed properly.
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
	}
}

// Handle and print incoming messages.
func handleMessages(listener *c4netioudp.Listener, npconn *c4netioudp.Conn, isHost bool) {
	for {
		msg, err := netpuncher.ReadFrom(npconn)
		if err != nil {
			log.WithError(err).Fatal("reading from netpuncher failed")
		}
		switch np := msg.(type) {
		case *netpuncher.AssID:
			log.Warnf("CID = %d", np.CID)
		case *netpuncher.CReq:
			log.WithField("packet", fmt.Sprintf("%+v", msg)).Infof("<- %T", msg)
			go func() {
				// Try to establish communication.
				if err = listener.Punch(&np.Addr, punchTimeout, punchInterval); err != nil {
					log.WithError(err).WithField("raddr", np.Addr.String()).Error("punching failed")
					return
				}
				if !isHost {
					log.WithField("raddr", np.Addr.String()).Info("connecting...")
					hostconn, err := listener.Dial(&np.Addr)
					if err != nil {
						log.WithError(err).Error("couldn't connect to host")
						return
					}
					defer hostconn.Close()
					log.WithField("raddr", np.Addr.String()).Info("connected successfully")
					_, err = hostconn.Write([]byte("Hello world!"))
					if err != nil {
						log.WithError(err).WithField("raddr", np.Addr.String()).Error("couldn't send message to host")
					}
				}
			}()
		case *netpuncher.CReqTCP:
			log.WithField("packet", fmt.Sprintf("%+v", msg)).Infof("<- %T", msg)
			go func() {
				log.WithField("raddr", np.DestAddr.String()).Info("connecting TCP...")
				conn, err := net.DialTCP("tcp6", &np.SourceAddr, &np.DestAddr)
				if err != nil {
					log.WithError(err).WithField("raddr", np.DestAddr.String()).Error("couldn't dial")
					return
				}
				defer conn.Close()
				log.WithField("raddr", np.DestAddr.String()).Info("connected TCP successfully")
				if !isHost {
					// send a message
					_, err = conn.Write([]byte("Hello TCP world!\n"))
					if err != nil {
						log.WithError(err).WithField("raddr", np.DestAddr.String()).Error("couldn't send TCP message to host")
					}
				} else {
					// receive message
					r := bufio.NewReader(conn)
					msg, err := r.ReadString('\n')
					if err != nil {
						log.WithError(err).WithField("raddr", np.DestAddr.String()).Error("couldn't read TCP message from client")
						return
					}
					log.WithField("raddr", np.DestAddr.String()).Infof("received: %s", msg)
				}
			}()
		default:
			log.WithField("packet", fmt.Sprintf("%+v", msg)).Infof("<- %T", msg)
		}
	}
}

// Handles new host connections.
func handleConn(listener *c4netioudp.Listener) {
	for {
		conn, err := listener.AcceptConn()
		if err != nil {
			log.WithError(err).Error("error in Accept")
			break
		}
		go func(conn *c4netioudp.Conn) {
			defer conn.Close()
			log.WithField("raddr", conn.RemoteAddr().String()).Info("new connection")
			var buf [100]byte
			n, err := conn.Read(buf[:])
			if err != nil {
				log.WithError(err).Error("error while reading")
				return
			}
			log.WithField("raddr", conn.RemoteAddr().String()).Infof("received: %s", string(buf[0:n]))
		}(conn)
	}
}
