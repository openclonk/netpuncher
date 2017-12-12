package c4netioudp

import (
	"net"
	"testing"
	"time"
)

// multiple connections from same address
func TestSequentialConnections(t *testing.T) {
	listener, err := Listen("udp", &net.UDPAddr{IP: net.IPv6loopback, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		listener.AcceptConn()
		listener.AcceptConn()
	}()

	done := make(chan bool, 1)
	go func() {
		raddr := listener.Addr().(*net.UDPAddr)
		c1, err := Dial("udp", nil, raddr)
		if err != nil {
			t.Fatal(err)
		}
		laddr := c1.LocalAddr().(*net.UDPAddr)
		c1.Close()

		// TODO: This is necessary because the listener also sends a Close
		// packet which confuses the following connection attempt.
		time.Sleep(10 * time.Millisecond)

		c2, err := Dial("udp", laddr, raddr)
		if err != nil {
			t.Fatal(err)
		}
		c2.Close()

		done <- true
	}()

	select {
	case <-done: // ok
	case <-time.After(1 * time.Second):
		t.Fatal("timeout")
	}
}
