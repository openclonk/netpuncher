package c4netioudp

import (
	"bytes"
	"container/list"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"sync/atomic"
	"time"
)

var ErrConnectionClosed = errors.New("connection closed")

type Conn struct {
	udp            *net.UDPConn
	writer         io.Writer       // write packets to me!
	raddr          *net.UDPAddr    // address we connect to
	laddr          *net.UDPAddr    // local address as seen by server
	rfuchan        chan rfu        // channel for receiving raw packets
	datachan       chan []byte     // channel for complete packages
	errchan        chan error      // channel for read errors
	sendchan       chan sendPacket // channel for outgoing packets
	quit           chan bool       // closed to signal goroutines
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
	if err = c.connect(); err != nil {
		return nil, fmt.Errorf("c4netioudp: error while connecting: %v", err)
	}
	go readFromUDP(c.udp, c.rfuchan, c.quit)
	go c.handlePackets()
	return c, nil
}

func (c *Conn) connect() error {
	// Three-way handshake
	// 1. ConnPacket --->
	connpkg := NewConnPacket(*c.raddr)
	_, err := connpkg.WriteTo(c.writer)
	if err != nil {
		return err
	}

	// 2. <--- ConnPacket
	buf := make([]byte, 1500)
	// TODO: Timeout, retries?
	nread, recvaddr, err := c.udp.ReadFromUDP(buf)
	if err != nil {
		return err
	}
	if nread < ConnPacketSize {
		return fmt.Errorf("ConnPacket not large enough")
	}
	hdr := ReadPacketHdr(buf)
	if hdr.StatusByte != IPID_Conn {
		return fmt.Errorf("received unexpected packet type %d", hdr.StatusByte)
	}
	connrepkg := ReadConnPacket(buf)
	if connrepkg.ProtocolVer != ProtocolVer {
		return fmt.Errorf("unsupported protocol version %d", connrepkg.ProtocolVer)
	}
	c.laddr = &connrepkg.Addr

	// 3. ConnOkPacket --->
	// TODO: Retransmission?
	connokpkg := NewConnOkPacket(*recvaddr)
	_, err = connokpkg.WriteTo(c.writer)
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
				ping := PacketHdr{StatusByte: IPID_Ping}
				_, _ = ping.WriteTo(c.writer)
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
		return 0, ErrConnectionClosed
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

func (c *Conn) Close() error {
	select {
	case <-c.quit:
		return fmt.Errorf("connection was already closed")
	default:
	}
	close(c.quit)
	// Send IPID_Close packet to server
	closePacket := NewClosePacket(*c.raddr)
	_, _ = closePacket.WriteTo(c.writer)
	if c.writer == c.udp {
		return c.udp.Close()
	}
	// We don't own the UDP socket, so we don't have to close it.
	return nil
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
