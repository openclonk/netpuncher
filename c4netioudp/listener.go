package c4netioudp

import (
	"fmt"
	"net"
	"time"
)

const connTimeout = 5 * time.Second // initial connection timeout (ConnPacket to ConnOkPacket)

type Listener struct {
	udp        *net.UDPConn
	acceptchan chan *Conn // channel for new connections
	closechan  chan *Conn // channel to signal a closing connection
	errchan    chan error // channel for UDP errors
	quit       chan bool  // closed to signal goroutines
	quithp     chan bool  // signal Close() to handlePackets()
}

func Listen(network string, laddr *net.UDPAddr) (*Listener, error) {
	l := Listener{
		acceptchan: make(chan *Conn, 32),
		closechan:  make(chan *Conn, 32),
		errchan:    make(chan error),
		quit:       make(chan bool),
		quithp:     make(chan bool),
	}
	var err error
	l.udp, err = net.ListenUDP(network, laddr)
	if err != nil {
		return nil, err
	}
	go l.handlePackets()
	return &l, nil
}

type udpkey string

// A UDP address cannot directly be used as map key as it contains a slice.
func addrkey(addr *net.UDPAddr) udpkey {
	// TODO: Is this safe?
	return udpkey(addr.String())
}

func (l *Listener) handlePackets() {
	rfuchan := make(chan rfu)
	go readFromUDP(l.udp, rfuchan, l.quit)
	// fully opened connections
	conns := make(map[udpkey]*Conn)
	// connections where we're still waiting for ConnOk
	connsinprogress := make(map[udpkey]*Conn)
	// connection timeouts
	conntimeout := make(chan udpkey)
	for {
		select {
		case <-l.quithp:
			for _, conn := range conns {
				// Empty the closechan to make sure conn.Close() doesn't block.
				select {
				case <-l.closechan:
				default:
				}
				conn.Close()
			}
			l.quithp <- true
			return
		case c := <-l.closechan:
			// Remove the channel from the map of open connections.
			delete(conns, addrkey(c.raddr))
		case r := <-rfuchan:
			if r.err != nil {
				l.errchan <- r.err
				continue
			}
			key := addrkey(r.addr)
			// Do we already have a connection for this address?
			conn := conns[key]
			// Decode packet to find connection attempts.
			hdr := ReadPacketHdr(r.buf)
			switch hdr.StatusByte & 0x7f {
			case IPID_Conn:
				if r.n < ConnPacketSize {
					continue
				}
				if conn != nil {
					// This is a re-connection, close the old connection.
					conn.closereason = "reconnection"
					conn.noclosepacket = true
					conn.Close()
				}
				// There's no need to read the initial ConnPacket, the client
				// does all version checks. We may need to read the packet here
				// in the future to support multiple protocol versions.
				writer := writerToUDP{l.udp, r.addr}
				connrepkg := NewConnPacket(*r.addr)
				connrepkg.WriteTo(writer)
				conn = newConn()
				conn.udp = l.udp
				conn.raddr = r.addr
				conn.writer = writer
				conn.closechan = l.closechan
				connsinprogress[key] = conn
				// Connection timeout
				go func(key udpkey) {
					time.Sleep(connTimeout)
					conntimeout <- key
				}(key)
			case IPID_ConnOK:
				if r.n < ConnOkPacketSize {
					continue
				}
				conn, ok := connsinprogress[key]
				if !ok {
					continue
				}
				// Nothing interesting in the packet itself as we don't support
				// multicast.
				delete(connsinprogress, key)
				conns[key] = conn
				go conn.handlePackets()
				l.acceptchan <- conn
			default:
				if conn != nil {
					conn.rfuchan <- r
				}
			}
		case key := <-conntimeout:
			delete(connsinprogress, key)
		}
	}
}

func (l *Listener) AcceptConn() (*Conn, error) {
	select {
	case conn := <-l.acceptchan:
		return conn, nil
	case err := <-l.errchan:
		return nil, err
	case <-l.quit:
		return nil, fmt.Errorf("c4netioudp: listener closed")
	}
}

func (l *Listener) Accept() (net.Conn, error) {
	return l.AcceptConn()
}

func (l *Listener) Close() error {
	close(l.quit)
	// handlePackets() has to close all connections before we can Close the UDP
	// socket.
	l.quithp <- true
	<-l.quithp
	return l.udp.Close()
}

func (l *Listener) Addr() net.Addr {
	return l.udp.LocalAddr()
}
