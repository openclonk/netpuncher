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
	dialchan   chan *Conn // channel for new outgoing connections
	errchan    chan error // channel for UDP errors
	quit       chan bool  // closed to signal goroutines
	quithp     chan bool  // signal Close() to handlePackets()
}

func Listen(network string, laddr *net.UDPAddr) (*Listener, error) {
	l := Listener{
		acceptchan: make(chan *Conn, 32),
		closechan:  make(chan *Conn, 32),
		dialchan:   make(chan *Conn),
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

func (l *Listener) newConnTo(raddr *net.UDPAddr) *Conn {
	writer := writerToUDP{l.udp, raddr}
	conn := newConn()
	conn.udp = l.udp
	conn.raddr = raddr
	conn.writer = writer
	conn.closechan = l.closechan
	return conn
}

func (l *Listener) handlePackets() {
	rfuchan := make(chan rfu)
	go readFromUDP(l.udp, rfuchan, l.quit)
	// fully opened connections
	conns := make(map[udpkey]*Conn)
	// incoming connections where we're still waiting for ConnOk
	connsinprogress := make(map[udpkey]*Conn)
	// outgoing connections - managed in Dial() and Conn
	dials := make(map[udpkey]*Conn)
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
			key := addrkey(c.raddr)
			delete(conns, key)
			delete(dials, key)
		case c := <-l.dialchan:
			dials[addrkey(c.raddr)] = c
		case r := <-rfuchan:
			if r.err != nil {
				l.errchan <- r.err
				continue
			}
			key := addrkey(r.addr)
			// Dials are always managed externally.
			if dial, ok := dials[key]; ok {
				dial.rfuchan <- r
				continue
			}
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
				conn := l.newConnTo(r.addr)
				connrepkg := NewConnPacket(*r.addr)
				connrepkg.WriteTo(conn.writer)
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

func (l *Listener) Punch(raddr *net.UDPAddr, timeout, interval time.Duration) error {
	// Create a temporary Conn to have packet forwarding from the listener.
	conn := l.newConnTo(raddr)
	conn.noclosepacket = true
	l.dialchan <- conn
	defer conn.Close()
	return conn.punch(timeout, interval)
}

func (l *Listener) Dial(raddr *net.UDPAddr) (*Conn, error) {
	conn := l.newConnTo(raddr)
	// Register with packet handler so that forwarding works.
	l.dialchan <- conn
	if err := conn.connect(); err != nil {
		return nil, fmt.Errorf("c4netioudp: error while connecting: %v", err)
	}
	go conn.handlePackets()
	return conn, nil
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
