package main

import (
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"

	"github.com/lluchs/netpuncher"
	"github.com/lluchs/netpuncher/c4netioudp"
	"github.com/lluchs/netpuncher/server"
)

func main() {
	listenaddr := net.UDPAddr{IP: net.IPv6unspecified, Port: 11115}
	if p, err := strconv.Atoi(os.Getenv("PORT")); err == nil {
		listenaddr.Port = p
	}

	server := server.Server{
		AcceptConn: func(c *server.Conn, err error) {
			if err != nil {
				log.Fatal("error during Accept: ", err)
			}
			log.Printf("connect: %v #%d\n", c.NetIOConn.RemoteAddr(), c.ID)
		},
		MarshalErr: func(err error) {
			log.Println(err)
		},
		UnsupportedVersionErr: func(c *server.Conn, err *netpuncher.ErrUnsupportedVersion) {
			log.Printf("client #%d: unsupported version %d", c.ID, err)
		},
		InvalidPacketErr: func(c *server.Conn, err error) {
			log.Printf("client #%d: couldn't read packet: %v", c.ID, err)
		},
		RegisterHost: func(host *server.Conn) {
			log.Printf("host: #%d", host.ID)
		},
		CReq: func(host *server.Conn, client *server.Conn) {
			log.Printf("CReq: client %v <--> host %v #%d\n", client.NetIOConn.RemoteAddr(), host.NetIOConn.RemoteAddr(), host.ID)
		},
		CloseConn: func(c *server.Conn, err *c4netioudp.ErrConnectionClosed) {
			log.Printf("close:   %v #%d (%s)\n", c.NetIOConn.RemoteAddr(), c.ID, err)
		},
	}

	err := server.Listen("udp", &listenaddr)
	if err != nil {
		log.Fatal("couldn't ListenUDP", err)
	}
	defer server.Close()
	log.Printf("netpuncher listening on %v", server.Addr())

	// Wait for an interrupt. Without this special handling, the connection
	// would not be closed properly.
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
}
