package netpuncher

import (
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
)

const (
	PID_Puncher_AssID = 0x51 // Puncher announcing ID to client
	PID_Puncher_SReq  = 0x52 // Client requesting to be served with punching (for an ID)
	PID_Puncher_CReq  = 0x53 // Puncher requesting clients to punch (towards an address)
)

// 1 byte type, CReq is largest (ASCII IP address)
const MaxPacketSize = 1 + 50

type PuncherPacket interface {
	Type() byte
	//encoding.BinaryMarshaler
}

// Encountered an unknown message type while decoding.
type ErrUnknownType byte

func (t ErrUnknownType) Error() string {
	return fmt.Sprintf("netpuncher: unknown message type 0x%x", t)
}

// Message not properly formatted.
type ErrInvalidMessage string

func (msg ErrInvalidMessage) Error() string {
	return fmt.Sprintf("netpuncher: %s", string(msg))
}

// Not read enough bytes for a full message.
type ErrNotReadEnough int

func (n ErrNotReadEnough) Error() string {
	return fmt.Sprintf("netpuncher: message not long enough, read %d byte", n)
}

var addrRegexp = regexp.MustCompile(`^UDP([0-9\.]+|\[[0-9a-f:]*\]):([0-9]+)$`)

// Reads one puncher message.
func ReadFrom(r io.Reader) (PuncherPacket, error) {
	buf := make([]byte, MaxPacketSize)
	n, err := r.Read(buf)
	if err != nil {
		return nil, err
	}
	if n < 2 {
		return nil, ErrNotReadEnough(n)
	}
	switch buf[0] {
	case PID_Puncher_AssID:
		id, err := strconv.ParseUint(string(buf[1:n]), 10, 32)
		if err != nil {
			return nil, ErrInvalidMessage(fmt.Sprintf("netpuncher: invalid ID in AssID: %v", err))
		}
		return &AssID{uint32(id)}, nil
	case PID_Puncher_SReq:
		id, err := strconv.ParseUint(string(buf[1:n]), 10, 32)
		if err != nil {
			return nil, ErrInvalidMessage(fmt.Sprintf("netpuncher: invalid ID in SReq: %v", err))
		}
		return &SReq{uint32(id)}, nil
	case PID_Puncher_CReq:
		m := addrRegexp.FindSubmatch(buf[1:n])
		if m == nil {
			return nil, ErrInvalidMessage("netpuncher: couldn't parse address in CReq")
		}
		ipstr := m[1]
		if ipstr[0] == '[' {
			ipstr = ipstr[1 : len(ipstr)-1]
		}
		ip := net.ParseIP(string(ipstr))
		if ip == nil {
			return nil, ErrInvalidMessage("netpuncher: couldn't parse IP in CReq")
		}
		port, err := strconv.ParseUint(string(m[2]), 10, 16)
		if err != nil {
			return nil, ErrInvalidMessage(fmt.Sprintf("netpuncher: couldn't parse port in CReq: %v", err))
		}
		addr := net.UDPAddr{IP: ip, Port: int(port)}
		return &CReq{addr}, nil
	}
	return nil, ErrUnknownType(buf[0])
}

type AssID struct {
	CID uint32
}

func (*AssID) Type() byte { return PID_Puncher_AssID }

type SReq struct {
	CID uint32
}

func (*SReq) Type() byte { return PID_Puncher_SReq }

type CReq struct {
	Addr net.UDPAddr
}

func (*CReq) Type() byte { return PID_Puncher_CReq }
