package c4netioudp

import (
	"bytes"
	"container/list"
	"fmt"
	"io"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apex/log"
)

const connRetransmissionTimeout = 500 * time.Millisecond

type ErrConnectionClosed string

func (reason ErrConnectionClosed) Error() string { return string(reason) }

type Conn struct {
	udp            *net.UDPConn
	writer         io.Writer       // write packets to me!
	raddr          *net.UDPAddr    // address we connect to
	laddr          *net.UDPAddr    // local address as seen by server
	rfuchan        chan rfu        // channel for receiving raw packets
	datachan       chan []byte     // channel for complete packages
	errchan        chan error      // channel for read errors
	sendchan       chan sendPacket // channel for outgoing packets
	closechan      chan *Conn      // channel to signal closing to Listener
	closemutex     sync.Mutex      // mutex protecting Close()
	quit           chan bool       // closed to signal goroutines
	closereason    string          // reason the connection was closed
	noclosepacket  bool            // whether to send a packet on Close()
	oPacketCounter uint32          // FNr of last outgoing packet
}

func newConn() *Conn {
	return &Conn{
		rfuchan:  make(chan rfu, 64),
		datachan: make(chan []byte, 32),
		errchan:  make(chan error),
		sendchan: make(chan sendPacket, 64), // should never block
		quit:     make(chan bool),
	}
}

func Dial(network string, laddr, raddr *net.UDPAddr) (*Conn, error) {
	c := newConn()
	c.raddr = raddr
	var err error
	c.udp, err = net.DialUDP(network, laddr, raddr)
	if err != nil {
		return nil, err
	}
	c.writer = c.udp
	go readFromUDP(c.udp, c.rfuchan, c.quit)
	if err = c.connect(); err != nil {
		c.quit <- true
		return nil, fmt.Errorf("c4netioudp: error while connecting: %v", err)
	}
	go c.handlePackets()
	return c, nil
}

// punch sends packets over the connection until we receive something or the
// timeout is reached.
func (c *Conn) punch(timeout, interval time.Duration) error {
	timeouttimer := time.NewTimer(timeout)
	intervaltimer := time.NewTimer(interval)
	sendMsg := func() {
		err := c.SendPing(c.raddr)
		if err != nil {
			// I'm sometimes getting `sendto: operation not permitted` errors
			// here that seem to be transient. Just log and ignore all errors.
			// Real errors will run into the timeout.
			log.WithError(err).WithField("raddr", c.raddr.String()).Debug("punch: send error")
		}
	}
	for {
		select {
		case <-intervaltimer.C:
			log.WithField("raddr", c.raddr.String()).Debug("punch: sending")
			sendMsg()
			intervaltimer.Reset(interval)
		case <-timeouttimer.C:
			log.WithField("raddr", c.raddr.String()).Debug("punch: timeout")
			return fmt.Errorf("timeout")
		case r := <-c.rfuchan:
			if r.err != nil {
				return r.err
			}
			log.WithField("raddr", c.raddr.String()).Debug("punch: success")
			// We received something, so we've punched through - the actual content doesn't matter.
			// Send one more message to signal the other side.
			sendMsg()
			return nil
		}
	}

}

// connect establishes a connection to another server.
func (c *Conn) connect() error {
	// Three-way handshake
	// 1. ConnPacket --->
	sendConnPacket := func() error {
		log.WithField("raddr", c.raddr.String()).Debug("connect: -> ConnPacket")
		connpkg := NewConnPacket(*c.raddr)
		_, err := connpkg.WriteTo(c.writer)
		return err
	}
	if err := sendConnPacket(); err != nil {
		return err
	}

	// 2. <--- ConnPacket
	// TODO: retries?
	var recvaddr *net.UDPAddr
	timeout := time.NewTimer(connTimeout)
	retrTimer := time.NewTimer(connRetransmissionTimeout)
	for recvaddr == nil {
		select {
		case <-timeout.C:
			return fmt.Errorf("connection timeout")
		case <-retrTimer.C:
			// Retransmit the initial packet in case it went missing.
			if err := sendConnPacket(); err != nil {
				return err
			}
		case r := <-c.rfuchan:
			if r.err != nil {
				return r.err
			}
			if r.n < ConnPacketSize {
				log.WithFields(log.Fields{
					"raddr": c.raddr.String(),
					"size":  r.n,
				}).Debug("connect: discarding too-small packet")
				//return fmt.Errorf("ConnPacket not large enough")
				continue
			}
			hdr := ReadPacketHdr(r.buf)
			if hdr.StatusByte != IPID_Conn {
				log.WithFields(log.Fields{
					"raddr": c.raddr.String(),
					"type":  hdr.StatusByte,
				}).Debug("connect: discarding unexpected packet")
				//return fmt.Errorf("received unexpected packet type %d", hdr.StatusByte)
				continue
			}
			connrepkg := ReadConnPacket(r.buf)
			if connrepkg.ProtocolVer != ProtocolVer {
				return fmt.Errorf("unsupported protocol version %d", connrepkg.ProtocolVer)
			}
			log.WithField("raddr", c.raddr.String()).Debug("connect: <- ConnRePacket")
			c.laddr = &connrepkg.Addr
			recvaddr = r.addr
		}
	}

	// 3. ConnOkPacket --->
	// TODO: Retransmission? Is this any ACK for this packet?
	log.WithField("raddr", c.raddr.String()).Debug("connect: -> ConnOkPacket")
	connokpkg := NewConnOkPacket(*recvaddr)
	_, err := connokpkg.WriteTo(c.writer)
	if err != nil {
		return err
	}

	// Done, we're connected now and can send/receive data
	return nil
}

type recvPacket struct {
	fragments    map[uint32][]byte
	size         uint32 // combined size of fragments
	completeSize uint32
}

func (p *recvPacket) assemble(fnr uint32) []byte {
	buf := make([]byte, 0, p.size)
	for nr := fnr; uint32(len(buf)) < p.size; nr++ {
		fragment, ok := p.fragments[nr]
		if !ok {
			panic("tried to assemble incomplete packet")
		}
		buf = append(buf, fragment...)
	}
	return buf
}

// Interval Check packets are sent in
const checkInterval = 1 * time.Second

// Connection timeout for established connections
// Usually, each side sends Check packets each second (see `checkInterval`), so
// not sending any data isn't an issue.
const connectionTimeout = 30 * time.Second

// Maximum number of asks per Check packet
const maxAsks = 10

func (c *Conn) handlePackets() {
	timeout := time.NewTimer(connectionTimeout)
	ticker := time.NewTicker(checkInterval)
	dpackets := make(map[uint32]*recvPacket)
	sendPackets := list.New()
	var IPacketCounter uint32  // FNr of next incoming packet
	var RIPacketCounter uint32 // from incoming Check packet
	for {
		select {
		case <-c.quit:
			ticker.Stop()
			return
		case <-ticker.C:
			// Time for a Check packet!
			ask := make(map[uint32]bool) // poor gopher's set
			// First, assume everything missing.
			for i := IPacketCounter; i < RIPacketCounter; i++ {
				ask[i] = true
			}
			// Now remove those packets we already received.
			for i := range dpackets {
				for nr := range dpackets[i].fragments {
					delete(ask, nr)
				}
			}
			// Gather everything into a slice.
			asks := make([]uint32, 0, maxAsks)
			for nr := range ask {
				asks = append(asks, nr)
				if len(asks) == maxAsks {
					break
				}
			}
			check := NewCheckPacketHdr(asks, IPacketCounter, c.oPacketCounter)
			_, _ = check.WriteTo(c.writer)
		case <-timeout.C:
			// Peer seems to be down, close connection.
			c.closereason = "connection timeout"
			c.Close()
		case r := <-c.rfuchan:
			if r.err != nil {
				c.errchan <- r.err
				continue
			}
			if r.n < PacketHdrSize {
				continue
			}
			hdr := ReadPacketHdr(r.buf)
			if hdr.Nr > RIPacketCounter {
				RIPacketCounter = hdr.Nr
			}
			switch hdr.StatusByte & 0x7f {
			case IPID_Ping:
				// Reply to ping, ignore errors.
				//ping := PacketHdr{StatusByte: IPID_Ping}
				//_, _ = ping.WriteTo(c.writer)
			case IPID_Data:
				if r.n < DataPacketHdrSize {
					continue
				}
				data := ReadDataPacketHdr(r.buf)
				if hdr.Nr < IPacketCounter {
					continue // duplicate packet
				}
				datasize := uint32(r.n - DataPacketHdrSize)
				pkt := dpackets[data.FNr]
				if pkt == nil {
					pkt = &recvPacket{
						fragments:    make(map[uint32][]byte),
						completeSize: data.Size,
					}
					dpackets[data.FNr] = pkt
				}
				pkt.fragments[hdr.Nr] = r.buf[DataPacketHdrSize:r.n]
				pkt.size += datasize
				// Assemble complete packets.
				if IPacketCounter == data.FNr {
					for {
						pkt, ok := dpackets[IPacketCounter]
						if ok && pkt.size >= pkt.completeSize {
							delete(dpackets, IPacketCounter)
							c.datachan <- pkt.assemble(IPacketCounter)
							IPacketCounter += uint32(len(pkt.fragments))
						} else {
							break
						}
					}
				}
			case IPID_Check:
				if r.n < CheckPacketHdrSize {
					continue
				}
				check := ReadCheckPacketHdr(r.buf)
				// Remove all ACKed packets.
				var next *list.Element
				for e := sendPackets.Front(); e != nil; e = next {
					next = e.Next()
					p := e.Value.(sendPacket)
					if p.fnr+uint32(len(p.fragments))-1 < check.AckNr {
						sendPackets.Remove(e)
					}
				}
				// Handle retransmission of packets in Ask.
				if len(check.Ask) > 0 {
					asks := uint32Slice(check.Ask)
					sort.Sort(asks)
					i := 0
					e := sendPackets.Front()
					for e != nil && i < len(asks) {
						ask := check.Ask[i]
						p := e.Value.(sendPacket)
						if ask >= p.fnr && ask < p.fnr+uint32(len(p.fragments)) {
							c.writeFragment(p.fragments[ask-p.fnr], ask, p.fnr, p.size)
							i++
						} else {
							e = e.Next()
						}
					}
				}
			case IPID_Close:
				// TODO: Decode packet and check Addr
				c.closereason = "connection closed by peer"
				c.noclosepacket = true
				c.Close()
			default:
				continue
			}
			// Reset connection timeout. Note that we only reach this code for
			// packets handled above.
			if !timeout.Stop() {
				<-timeout.C
			}
			timeout.Reset(connectionTimeout)
		case pkt := <-c.sendchan:
			// Save the packet for potential retransmission later on.
			// Insert in the right spot which may not be at the end (race
			// condition between allocating sequence numbers and sending to
			// the channel).
			haveInserted := false
			for e := sendPackets.Back(); e != nil; e = e.Prev() {
				if e.Value.(sendPacket).fnr < pkt.fnr {
					sendPackets.InsertAfter(pkt, e)
					haveInserted = true
					break
				}
			}
			if !haveInserted {
				sendPackets.PushFront(pkt)
			}
		}
	}
}

// Reads a full message from c.
func (c *Conn) Read(b []byte) (n int, err error) {
	select {
	case data := <-c.datachan:
		copy(b, data)
		return len(data), nil
	case err = <-c.errchan:
		return
	case <-c.quit:
		return 0, ErrConnectionClosed(c.closereason)
	}
}

type sendPacket struct {
	fragments [][]byte
	fnr, size uint32
}

func (c *Conn) writeFragment(frag []byte, nr, fnr, size uint32) {
	datapkt := NewDataPacketHdr(nr, fnr, size)
	var buf bytes.Buffer
	datapkt.WriteTo(&buf)
	buf.Write(frag)
	buf.WriteTo(c.writer)
}

// Write a full message to c.
func (c *Conn) Write(b []byte) (n int, err error) {
	select {
	case <-c.quit:
		return 0, ErrConnectionClosed(c.closereason)
	default:
	}
	cnt := FragmentCnt(len(b))
	// Allocate sequence numbers for all fragments.
	fnr := atomic.AddUint32(&c.oPacketCounter, uint32(cnt)) - uint32(cnt)
	// Copy the buffer as we have to keep the data for retransmissions.
	bc := append([]byte(nil), b...)
	size := uint32(len(b))
	fragments := make([][]byte, cnt)
	for i := 0; i < cnt; i++ {
		high := (i + 1) * MaxDataSize
		if high > len(b) {
			high = len(b)
		}
		fragments[i] = bc[i*MaxDataSize : high]
		c.writeFragment(fragments[i], fnr+uint32(i), fnr, size)
	}
	// Move the packet over to the handlePackets loop for retransmissions.
	c.sendchan <- sendPacket{
		fragments: fragments,
		fnr:       fnr,
		size:      size,
	}
	return len(b), nil
}

// SendPing sends an IPID_Ping message to raddr.
// A remote Clonk instance will reply with another IPID_Ping message.
func (c *Conn) SendPing(raddr *net.UDPAddr) error {
	ping := PacketHdr{StatusByte: IPID_Ping}
	var buf bytes.Buffer
	_, err := ping.WriteTo(&buf)
	// (writing to bytes.Buffer cannot fail)
	_, err = c.udp.WriteToUDP(buf.Bytes(), raddr)
	return err
}

// SendTest sends an IPID_Test message to raddr.
// The test packet does not have any function and will be discarded silently.
func (c *Conn) SendTest(raddr *net.UDPAddr) error {
	ping := PacketHdr{StatusByte: IPID_Test}
	var buf bytes.Buffer
	_, err := ping.WriteTo(&buf)
	// (writing to bytes.Buffer cannot fail)
	_, err = c.udp.WriteToUDP(buf.Bytes(), raddr)
	return err
}

func (c *Conn) Close() error {
	// Closing a channel twice panics, so we have to protect this with a mutex.
	c.closemutex.Lock()
	defer c.closemutex.Unlock()
	select {
	case <-c.quit:
		return ErrConnectionClosed(c.closereason)
	default:
	}
	close(c.quit)

	if c.closereason == "" {
		c.closereason = "connection closed locally"
	}
	if !c.noclosepacket {
		// Send IPID_Close packet to server
		closePacket := NewClosePacket(*c.raddr)
		_, _ = closePacket.WriteTo(c.writer)
	}
	var err error
	if c.writer == c.udp {
		err = c.udp.Close()
	} else {
		// We don't own the UDP socket, so we don't have to close it.
		// However, signal us closing to the listener to stop packet
		// delivery.
		c.closechan <- c
	}
	return err
}

func (c *Conn) LocalAddr() net.Addr {
	return c.udp.LocalAddr()
}

func (c *Conn) RemoteAddr() net.Addr {
	return c.raddr
}

func (c *Conn) SetDeadline(t time.Time) error {
	return c.udp.SetDeadline(t)
}

func (c *Conn) SetReadDeadline(t time.Time) error {
	return c.udp.SetReadDeadline(t)
}

func (c *Conn) SetWriteDeadline(t time.Time) error {
	return c.udp.SetWriteDeadline(t)
}

// For sorting with sort package.
type uint32Slice []uint32

func (p uint32Slice) Len() int           { return len(p) }
func (p uint32Slice) Less(i, j int) bool { return p[i] < p[j] }
func (p uint32Slice) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }
