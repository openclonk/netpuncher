package c4netioudp

import "bytes"
import "encoding/binary"
import "io"
import "net"

// struct BinAddr
// {
// 	uint16_t port;
// 	uint8_t type{0};
// 	union
// 	{
// 		uint8_t v4[4];
// 		uint8_t v6[16];
// 	};
// };
const binAddrSize = 2 + 1 + 16

func readBinAddr(b []byte) (addr net.UDPAddr) {
	addr.Port = int(binary.LittleEndian.Uint16(b[0:]))
	switch b[2] {
	case 1: // IPv4
		addr.IP = net.IPv4(b[3], b[4], b[5], b[6])
	case 2: // IPv6
		addr.IP = append([]byte(nil), b[3:19]...)
	}
	return
}

// Note: assumes that writing can't fail, i.e. w has to be a bytes.Buffer
func writeBinAddr(w io.Writer, addr *net.UDPAddr) {
	binary.Write(w, binary.LittleEndian, uint16(addr.Port))
	if v4 := addr.IP.To4(); v4 != nil {
		w.Write([]byte{1})
		w.Write(v4)
		// Pad the rest of the union with 0
		w.Write(net.IPv6unspecified[4:])
	} else {
		v6 := addr.IP.To16()
		if v6 == nil {
			panic("invalid IP address")
		}
		w.Write([]byte{2})
		w.Write(v6)
	}
}

const PacketHdrSize = 1 + 4

const (
	IPID_Ping    = 0
	IPID_Test    = 1
	IPID_Conn    = 2
	IPID_ConnOK  = 3
	IPID_AddAddr = 7
	IPID_Data    = 4
	IPID_Check   = 5
	IPID_Close   = 6
)

type PacketHdr struct {
	StatusByte uint8
	Nr         uint32 // packet nr
}

func ReadPacketHdr(b []byte) PacketHdr {
	var hdr PacketHdr
	hdr.StatusByte = b[0]
	hdr.Nr = binary.LittleEndian.Uint32(b[1:])
	return hdr
}

func (hdr *PacketHdr) WriteTo(w io.Writer) (n int64, err error) {
	var buf [PacketHdrSize]byte
	buf[0] = hdr.StatusByte
	binary.LittleEndian.PutUint32(buf[1:], hdr.Nr)
	written, err := w.Write(buf[:])
	return int64(written), err
}

const ConnPacketSize = PacketHdrSize + 4 + 2*binAddrSize
const ProtocolVer = 2

type ConnPacket struct {
	PacketHdr
	ProtocolVer uint32
	Addr        net.UDPAddr
	MCAddr      net.UDPAddr
}

func NewConnPacket(addr net.UDPAddr) ConnPacket {
	return ConnPacket{
		PacketHdr:   PacketHdr{StatusByte: IPID_Conn},
		ProtocolVer: ProtocolVer,
		Addr:        addr,
		MCAddr:      net.UDPAddr{IP: net.IPv6unspecified},
	}
}

func ReadConnPacket(b []byte) (pkg ConnPacket) {
	pkg.PacketHdr = ReadPacketHdr(b)
	pkg.ProtocolVer = binary.LittleEndian.Uint32(b[PacketHdrSize:])
	pkg.Addr = readBinAddr(b[PacketHdrSize+4:])
	pkg.MCAddr = readBinAddr(b[PacketHdrSize+4+binAddrSize:])
	return
}

func (pkg *ConnPacket) WriteTo(w io.Writer) (n int64, err error) {
	var buf bytes.Buffer
	buf.Grow(ConnPacketSize)
	pkg.PacketHdr.WriteTo(&buf)
	binary.Write(&buf, binary.LittleEndian, pkg.ProtocolVer)
	writeBinAddr(&buf, &pkg.Addr)
	writeBinAddr(&buf, &pkg.MCAddr)
	if buf.Len() != ConnPacketSize {
		panic("ConnPacket has invalid size")
	}
	return buf.WriteTo(w)
}

// Values for ConnOkPacket.MCMode
const (
	MCM_NoMC = iota
	MCM_MC
	MCM_MCOK
)

const ConnOkPacketSize = PacketHdrSize + 4 + binAddrSize

type ConnOkPacket struct {
	PacketHdr
	MCMode uint32
	Addr   net.UDPAddr
}

func NewConnOkPacket(addr net.UDPAddr) ConnOkPacket {
	return ConnOkPacket{
		PacketHdr: PacketHdr{StatusByte: IPID_ConnOK},
		MCMode:    MCM_NoMC,
		Addr:      addr,
	}
}

func ReadConnOkPacket(b []byte) (pkg ConnOkPacket) {
	pkg.PacketHdr = ReadPacketHdr(b)
	pkg.MCMode = binary.LittleEndian.Uint32(b[PacketHdrSize:])
	pkg.Addr = readBinAddr(b[PacketHdrSize+4:])
	return
}

func (pkg *ConnOkPacket) WriteTo(w io.Writer) (n int64, err error) {
	var buf bytes.Buffer
	buf.Grow(ConnOkPacketSize)
	pkg.PacketHdr.WriteTo(&buf)
	binary.Write(&buf, binary.LittleEndian, pkg.MCMode)
	writeBinAddr(&buf, &pkg.Addr)
	if buf.Len() != ConnOkPacketSize {
		panic("ConnOkPacket has invalid size")
	}
	return buf.WriteTo(w)
}

// I think we can ignore that one as it's only important for peer-to-peer
// communication.
type AddAddrPacket struct {
	PacketHdr
	Addr    net.UDPAddr
	NewAddr net.UDPAddr
}

const DataPacketHdrSize = PacketHdrSize + 2*4

type DataPacketHdr struct {
	PacketHdr
	FNr  uint32 // start fragment of this series
	Size uint32 // packet size (all fragments)
}

func NewDataPacketHdr(fnr, size uint32) DataPacketHdr {
	return DataPacketHdr{
		PacketHdr: PacketHdr{StatusByte: IPID_Data},
		FNr:       fnr,
		Size:      size,
	}
}

func ReadDataPacketHdr(b []byte) (pkg DataPacketHdr) {
	pkg.PacketHdr = ReadPacketHdr(b)
	pkg.FNr = binary.LittleEndian.Uint32(b[PacketHdrSize:])
	pkg.Size = binary.LittleEndian.Uint32(b[PacketHdrSize+4:])
	return
}

func (pkg *DataPacketHdr) WriteTo(w io.Writer) (n int64, err error) {
	var buf bytes.Buffer
	buf.Grow(DataPacketHdrSize)
	pkg.PacketHdr.WriteTo(&buf)
	binary.Write(&buf, binary.LittleEndian, pkg.FNr)
	binary.Write(&buf, binary.LittleEndian, pkg.Size)
	if buf.Len() != DataPacketHdrSize {
		panic("DataPacketHdr has invalid size")
	}
	return buf.WriteTo(w)
}

const CheckPacketHdrSize = PacketHdrSize + 4*4

type CheckPacketHdr struct {
	PacketHdr
	//AskCount, MCAskCount uint32
	Ask, MCAsk     []uint32
	AckNr, MCAckNr uint32 // numbers of the last packets received
}

func NewCheckPacketHdr(ask []uint32, acknr, outacknr uint32) CheckPacketHdr {
	return CheckPacketHdr{
		PacketHdr: PacketHdr{StatusByte: IPID_Check, Nr: outacknr},
		Ask:       ask,
		AckNr:     acknr,
	}
}

func ReadCheckPacketHdr(b []byte) (pkg CheckPacketHdr) {
	pkg.PacketHdr = ReadPacketHdr(b)
	AskCount := binary.LittleEndian.Uint32(b[DataPacketHdrSize:])
	MCAskCount := binary.LittleEndian.Uint32(b[DataPacketHdrSize+4:])
	pkg.AckNr = binary.LittleEndian.Uint32(b[DataPacketHdrSize+8:])
	pkg.MCAckNr = binary.LittleEndian.Uint32(b[DataPacketHdrSize+12:])
	// Read Ask and MCAsk arrays following the header.
	// TODO: Verify that b is large enough.
	pkg.Ask = make([]uint32, AskCount)
	pkg.MCAsk = make([]uint32, MCAskCount)
	pos := CheckPacketHdrSize
	for i := uint32(0); i < AskCount; i++ {
		pkg.Ask[i] = binary.LittleEndian.Uint32(b[pos:])
		pos += 4
	}
	for i := uint32(0); i < MCAskCount; i++ {
		pkg.MCAsk[i] = binary.LittleEndian.Uint32(b[pos:])
		pos += 4
	}
	return
}

func (pkg *CheckPacketHdr) WriteTo(w io.Writer) (n int64, err error) {
	var buf bytes.Buffer
	buf.Grow(CheckPacketHdrSize + 4*len(pkg.Ask) + 4*len(pkg.MCAsk))
	pkg.PacketHdr.WriteTo(&buf)
	binary.Write(&buf, binary.LittleEndian, uint32(len(pkg.Ask)))
	binary.Write(&buf, binary.LittleEndian, uint32(len(pkg.MCAsk)))
	binary.Write(&buf, binary.LittleEndian, pkg.AckNr)
	binary.Write(&buf, binary.LittleEndian, pkg.MCAckNr)
	if buf.Len() != CheckPacketHdrSize {
		panic("CheckPacketHdr has invalid size")
	}
	binary.Write(&buf, binary.LittleEndian, pkg.Ask)
	binary.Write(&buf, binary.LittleEndian, pkg.MCAsk)
	return buf.WriteTo(w)
}
