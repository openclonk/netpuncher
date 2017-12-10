package main

import (
	"log"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/lluchs/netpuncher"
	"github.com/lluchs/netpuncher/c4netioudp"
)

type Conn struct {
	id      uint32
	c       *c4netioudp.Conn
	version netpuncher.ProtocolVersion
}

func (c *Conn) npHeader() netpuncher.Header {
	return netpuncher.Header{Version: c.version}
}

func (c *Conn) handlePackets(req chan<- punchReq, close chan<- uint32) {
	for {
		msg, err := netpuncher.ReadFrom(c.c)
		switch errt := err.(type) {
		case c4netioudp.ErrConnectionClosed:
			log.Printf("close:   %v #%d (%s)\n", c.c.RemoteAddr(), c.id, errt)
			c.c.Close()
			close <- c.id
			return
		case netpuncher.ErrUnsupportedVersion:
			log.Printf("client #%d: unsupported version %d", c.id, errt)
			c.c.Close()
			continue
		case nil: // ok
		default:
			log.Printf("couldn't read packet: %v", err)
			continue
		}
		switch np := msg.(type) {
		case *netpuncher.IDReq:
			c.version = np.Header.Version
			buf, err := netpuncher.AssID{Header: c.npHeader(), CID: c.id}.MarshalBinary()
			if err != nil {
				log.Printf("AssID.MarshalBinary(): %v", err)
			}
			c.c.Write(buf)
			log.Printf("host: #%d", c.id)
		case *netpuncher.SReq:
			c.version = np.Header.Version
			req <- punchReq{np.CID, c}
		}
	}
}

type punchReq struct {
	id   uint32
	conn *Conn
}

func main() {
	listenaddr := net.UDPAddr{IP: net.IPv6unspecified, Port: 11115}
	if p, err := strconv.Atoi(os.Getenv("PORT")); err == nil {
		listenaddr.Port = p
	}
	listener, err := c4netioudp.Listen("udp", &listenaddr)
	if err != nil {
		log.Fatal("couldn't ListenUDP", err)
	}
	defer listener.Close()
	log.Printf("netpuncher listening on %v", listener.Addr())

	rand.Seed(time.Now().UnixNano())

	go func() {
		connch := make(chan *c4netioudp.Conn)
		conns := make(map[uint32]*Conn)
		req := make(chan punchReq)
		closech := make(chan uint32)
		go func() {
			for {
				conn, err := listener.AcceptConn()
				if err != nil {
					log.Fatal("error during Accept: ", err)
				}
				connch <- conn
			}
		}()
		for {
			select {
			case conn := <-connch:
				id := rand.Uint32()
				c := &Conn{id: id, c: conn}
				conns[id] = c
				go c.handlePackets(req, closech)
				log.Printf("connect: %v #%d\n", conn.RemoteAddr(), id)
			case r := <-req:
				// The client (r.conn) requests punching from the host (r.id). We will send a
				// CReq message to both parties.
				client := r.conn
				if host, ok := conns[r.id]; ok {
					caddr := client.c.RemoteAddr().(*net.UDPAddr)
					haddr := host.c.RemoteAddr().(*net.UDPAddr)
					buf, err := netpuncher.CReq{Header: host.npHeader(), Addr: *caddr}.MarshalBinary()
					if err != nil {
						log.Printf("CReq.MarshalBinary(): %v", err)
						continue
					}
					host.c.Write(buf)
					buf, err = netpuncher.CReq{Header: client.npHeader(), Addr: *haddr}.MarshalBinary()
					if err != nil {
						log.Printf("CReq.MarshalBinary(): %v", err)
						continue
					}
					client.c.Write(buf)
					log.Printf("CReq: client %v <--> host %v #%d\n", caddr, haddr, r.id)
				}
			case id := <-closech:
				delete(conns, id)
			}
		}
	}()

	// Wait for an interrupt. Without this special handling, the connection
	// would not be closed properly.
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
}
