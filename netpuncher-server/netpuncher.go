package main

import (
	"log"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/lluchs/netpuncher"
	"github.com/lluchs/netpuncher/c4netioudp"
)

type Conn struct {
	id uint32
	c  *c4netioudp.Conn
}

func (c *Conn) handlePackets(req chan<- punchReq, close chan<- uint32) {
	for {
		msg, err := netpuncher.ReadFrom(c.c)
		if err == c4netioudp.ErrConnectionClosed {
			log.Printf("close: %v #%d\n", c.c.RemoteAddr(), c.id)
			c.c.Close()
			close <- c.id
			return
		} else if err != nil {
			log.Printf("couldn't read packet:", err)
			continue
		}
		switch np := msg.(type) {
		case *netpuncher.SReq:
			req <- punchReq{np.CID, c.c.RemoteAddr().(*net.UDPAddr)}
		}
	}
}

type punchReq struct {
	id   uint32
	addr *net.UDPAddr
}

func main() {
	listenaddr := net.UDPAddr{IP: net.IPv6unspecified, Port: 11115}
	listener, err := c4netioudp.Listen("udp", &listenaddr)
	if err != nil {
		log.Fatal("couldn't ListenUDP", err)
	}
	defer listener.Close()
	log.Println("netpuncher listening on port", listenaddr.Port)

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
				c := &Conn{id, conn}
				conns[id] = c
				go c.handlePackets(req, closech)
				buf, _ := netpuncher.AssID{id}.MarshalBinary()
				conn.Write(buf)
				log.Printf("connect: %v #%d\n", conn.RemoteAddr(), id)
			case r := <-req:
				if c, ok := conns[r.id]; ok {
					buf, _ := netpuncher.CReq{*r.addr}.MarshalBinary()
					c.c.Write(buf)
					log.Printf("CReq: %d ---> %v\n", r.id, r.addr)
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
