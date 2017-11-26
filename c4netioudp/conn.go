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
}

func Dial(network string, laddr, raddr *net.UDPAddr) (*Conn, error) {
	c := Conn{
		raddr:    raddr,
		datachan: make(chan []byte, 32),
		errchan:  make(chan error),
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

func (c *Conn) handlePackets() {
	buf := make([]byte, 1500)
	dpackets := make(map[uint32]*packet)
	var packetCounter uint32 // FNr of next packet
	for {
		nread, _, err := c.udp.ReadFromUDP(buf)
		if err != nil {
			c.errchan <- err
			continue
		}
		if nread < PacketHdrSize {
			continue
		}
		hdr := ReadPacketHdr(buf)
		switch hdr.StatusByte & 0x7f {
		case IPID_Ping:
			// Reply to ping, ignore errors.
			ping := PacketHdr{StatusByte: IPID_Ping}
			_, _ = ping.WriteTo(c.udp)
		case IPID_Data:
			if nread < DataPacketHdrSize {
				continue
			}
			data := ReadDataPacketHdr(buf)
			if hdr.Nr < packetCounter {
				continue // duplicate packet
			}
			datasize := uint32(nread - DataPacketHdrSize)
			pkt := dpackets[data.FNr]
			if pkt == nil {
				pkt = &packet{
					fragments:    make(map[uint32][]byte),
					completeSize: data.Size,
				}
				dpackets[data.FNr] = pkt
			}
			pkt.fragments[hdr.Nr] = append([]byte(nil), buf[DataPacketHdrSize:nread]...)
			pkt.size += datasize
			// Assemble complete packets.
			if packetCounter == data.FNr {
				for {
					pkt, ok := dpackets[packetCounter]
					if ok && pkt.size >= pkt.completeSize {
						delete(dpackets, packetCounter)
						c.datachan <- pkt.assemble(packetCounter)
						packetCounter += uint32(len(pkt.fragments))
					} else {
						break
					}
				}
			}
		case IPID_Check:
			if nread < CheckPacketHdrSize {
				continue
			}
			check := ReadCheckPacketHdr(buf)
			// TODO: Handle retransmission of sent packages
			fmt.Println("received check", check)
		case IPID_Close:
			// TODO: Decode packet and check Addr
			c.Close()
			return
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
