package c4netioudp

import (
	"fmt"
	"net"
	"time"
)

type Conn struct {
	udp      *net.UDPConn
	raddr    *net.UDPAddr // address we connect to
	laddr    *net.UDPAddr // local address as seen by server
	datachan chan []byte  // channel for complete packages
	errchan  chan error   // channel for read errors
	quit     chan bool    // closed to signal goroutines
}

func Dial(network string, laddr, raddr *net.UDPAddr) (*Conn, error) {
	c := Conn{
		raddr:    raddr,
		datachan: make(chan []byte, 32),
		errchan:  make(chan error),
		quit:     make(chan bool),
	}
	var err error
	c.udp, err = net.DialUDP(network, laddr, raddr)
	if err != nil {
		return nil, err
	}
	if err = c.connect(); err != nil {
		return nil, fmt.Errorf("c4netioudp: error while connecting: %v", err)
	}
	go c.handlePackets()
	return &c, nil
}

func (c *Conn) connect() error {
	// Three-way handshake
	// 1. ConnPacket --->
	connpkg := NewConnPacket(*c.raddr)
	_, err := connpkg.WriteTo(c.udp)
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
	_, err = connokpkg.WriteTo(c.udp)
	if err != nil {
		return err
	}

	// Done, we're connected now and can send/receive data
	return nil
}

type packet struct {
	fragments    map[uint32][]byte
	size         uint32 // combined size of fragments
	completeSize uint32
}

func (p *packet) assemble(fnr uint32) []byte {
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

// Return value from ReadFromUDP
type rfu struct {
	buf []byte
	n   int
	err error
}

// Interval Check packets are sent in
const checkInterval = 1 * time.Second

// Maximum number of asks per Check packet
const maxAsks = 10

func (c *Conn) handlePackets() {
	rfuchan := make(chan rfu)
	go func() {
		for {
			var r rfu
			r.buf = make([]byte, 1500)
			r.n, _, r.err = c.udp.ReadFromUDP(r.buf)
			select {
			case rfuchan <- r: // ok
			case <-c.quit:
				return
			}
		}
	}()
	ticker := time.NewTicker(checkInterval)
	dpackets := make(map[uint32]*packet)
	var IPacketCounter uint32  // FNr of next incoming packet
	var RIPacketCounter uint32 // from incoming Check packet
	var OPacketCounter uint32  // FNr of last outgoing packet
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
			check := NewCheckPacketHdr(asks, IPacketCounter, OPacketCounter)
			_, _ = check.WriteTo(c.udp)
		case r := <-rfuchan:
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
				_, _ = ping.WriteTo(c.udp)
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
					pkt = &packet{
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
				// TODO: Handle retransmission of sent packages
				fmt.Println("received check", check)
			case IPID_Close:
				// TODO: Decode packet and check Addr
				c.Close()
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
	}
}

func (c *Conn) Write(b []byte) (n int, err error) {
	panic("not implemented")
}

func (c *Conn) Close() error {
	close(c.quit)
	// TODO: Send IPID_Close packet to server
	return c.udp.Close()
}

func (c *Conn) LocalAddr() net.Addr {
	return c.udp.LocalAddr()
}

func (c *Conn) RemoteAddr() net.Addr {
	return c.udp.RemoteAddr()
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
