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
	c1closed := make(chan bool)
	go func() {
		c1, _ := listener.AcceptConn()
		c1.Read(nil)
		c1closed <- true
		listener.AcceptConn()
	}()

	done := make(chan bool, 1)
	fail := make(chan error)
	go func() {
		raddr := listener.Addr().(*net.UDPAddr)
		c1, err := Dial("udp", nil, raddr)
		if err != nil {
			t.Error(err)
			fail <- err
			return
		}
		laddr := c1.LocalAddr().(*net.UDPAddr)
		c1.Close()

		// Make sure the connection is closed properly before proceeding to
		// prevent "address already in use" error.
		<-c1closed

		c2, err := Dial("udp", laddr, raddr)
		if err != nil {
			t.Error(err)
			fail <- err
			return
		}
		c2.Close()

		done <- true
	}()

	select {
	case <-done: // ok
	case <-fail:
		t.FailNow()
	case <-time.After(1 * time.Second):
		t.Fatal("timeout")
	}
}

// Conn packet for active connection
func TestReconnecting(t *testing.T) {
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
			t.Error(err)
			return
		}
		laddr := c1.LocalAddr().(*net.UDPAddr)
		// Simulate a crashing client: UDP socket is released, but no Close packet.
		c1.udp.Close()

		// Second connection should succeed.
		c2, err := Dial("udp", laddr, raddr)
		if err != nil {
			t.Error(err)
			return
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
