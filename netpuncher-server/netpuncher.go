package main

import (
	"log"
	"net"
	"os"
	"os/signal"

	"github.com/lluchs/netpuncher/c4netioudp"
)

func main() {
	listenaddr := net.UDPAddr{IP: net.IPv6unspecified, Port: 11115}
	listener, err := c4netioudp.Listen("udp", &listenaddr)
	if err != nil {
		log.Fatal("couldn't ListenUDP", err)
	}
	defer listener.Close()
	log.Println("netpuncher listening on port", listenaddr.Port)

	go func() {
		for {
			conn, err := listener.AcceptConn()
			if err != nil {
				log.Fatal("error during Accept", err)
			}
			log.Println("got connection", conn.RemoteAddr())
			conn.Close()
		}
	}()

	// Wait for an interrupt. Without this special handling, the connection
	// would not be closed properly.
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
}
