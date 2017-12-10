// Protocol flow
// =============
//
//      *Host*                                  *Netpuncher*                               *Client*
//      ([2001:db8::2]:11113)                                                              ([2001:db8::1]:11113)
//
//      C4NetIOUDP Connect <----------------->
//
//      IDReq ------------------------------->
//
//            <-------------------------------  AssID[1337]
//      (announce on master server)
//
//                                                          <-------------------------->   C4NetIOUDP Connect
//
//                                                          <---------------------------   SReq[1337]
//
//            <-------------------------------  CReq["[2001:db8::1]:11113"]
//                                              CReq["[2001:db8::2]:11113"] ----------->   (ignores this message, I think?)
//
//      PID_Pong ---------------------------------------------------------------------->
//
package netpuncher

import (
	"bytes"
	"encoding"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
)

const (
	PID_Puncher_AssID = 0x51 // Puncher announcing ID to client
	PID_Puncher_SReq  = 0x52 // Client requesting to be served with punching (for an ID)
	PID_Puncher_CReq  = 0x53 // Puncher requesting clients to punch (towards an address)
	PID_Puncher_IDReq = 0x54 // Client requesting an ID
)

// 2 byte header, CReq is largest (port and IP)
const MaxPacketSize = 2 + 18

type PuncherPacket interface {
	Type() byte
	encoding.BinaryMarshaler
	encoding.BinaryUnmarshaler
}

// Encountered an unknown message type while decoding.
type ErrUnknownType byte

func (t ErrUnknownType) Error() string {
	return fmt.Sprintf("netpuncher: unknown message type 0x%x", t)
}

// Message has an unsupported protocol version
type ErrUnsupportedVersion ProtocolVersion

func (v ErrUnsupportedVersion) Error() string {
	return fmt.Sprintf("netpuncher: unsupported protocol version %d", int(v))
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
	var p PuncherPacket
	switch buf[0] {
	case PID_Puncher_AssID:
		p = &AssID{}
	case PID_Puncher_SReq:
		p = &SReq{}
	case PID_Puncher_CReq:
		p = &CReq{}
	case PID_Puncher_IDReq:
		p = &IDReq{}
	default:
		return nil, ErrUnknownType(buf[0])
	}
	if err = p.UnmarshalBinary(buf); err != nil {
		return nil, err
	}
	return p, nil
}

type ProtocolVersion byte

// Newest version supported
var NewestProtocolVersion = ProtocolVersion(1)

// Returns whether the implementation supports the protocol version.
func (v ProtocolVersion) Supported() bool {
	return v == 1
}

// Header preceding all messages.
type Header struct {
	Type    byte // See PID_Puncher_* constants
	Version ProtocolVersion
}

func (h Header) MarshalBinary() ([]byte, error) {
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, h)
	return b.Bytes(), nil
}

func (h *Header) UnmarshalBinary(buf []byte) error {
	b := bytes.NewReader(buf)
	err := binary.Read(b, binary.LittleEndian, h)
	if err != nil {
		return ErrInvalidMessage(err.Error())
	}
	if !h.Version.Supported() {
		return ErrUnsupportedVersion(h.Version)
	}
	return nil
}

type IDReq struct {
	Header
}

func (*IDReq) Type() byte { return PID_Puncher_IDReq }

func (p IDReq) MarshalBinary() ([]byte, error) {
	var b bytes.Buffer
	p.Header.Type = p.Type()
	binary.Write(&b, binary.LittleEndian, p)
	return b.Bytes(), nil
}

func (p *IDReq) UnmarshalBinary(buf []byte) error {
	b := bytes.NewReader(buf)
	err := binary.Read(b, binary.LittleEndian, p)
	if err != nil {
		return ErrInvalidMessage(err.Error())
	}
	if !p.Header.Version.Supported() {
		return ErrUnsupportedVersion(p.Header.Version)
	}
	return nil
}

type AssID struct {
	Header
	CID uint32
}

func (*AssID) Type() byte { return PID_Puncher_AssID }

// error is always nil
func (p AssID) MarshalBinary() ([]byte, error) {
	var b bytes.Buffer
	p.Header.Type = p.Type()
	binary.Write(&b, binary.LittleEndian, p)
	return b.Bytes(), nil
}

func (p *AssID) UnmarshalBinary(buf []byte) error {
	b := bytes.NewReader(buf)
	err := binary.Read(b, binary.LittleEndian, p)
	if err != nil {
		return ErrInvalidMessage(err.Error())
	}
	if !p.Header.Version.Supported() {
		return ErrUnsupportedVersion(p.Header.Version)
	}
	return nil
}

type SReq struct {
	Header
	CID uint32
}

func (*SReq) Type() byte { return PID_Puncher_SReq }

// error is always nil
func (p SReq) MarshalBinary() ([]byte, error) {
	var b bytes.Buffer
	p.Header.Type = p.Type()
	binary.Write(&b, binary.LittleEndian, p)
	return b.Bytes(), nil
}

func (p *SReq) UnmarshalBinary(buf []byte) error {
	b := bytes.NewReader(buf)
	err := binary.Read(b, binary.LittleEndian, p)
	if err != nil {
		return ErrInvalidMessage(err.Error())
	}
	if !p.Header.Version.Supported() {
		return ErrUnsupportedVersion(p.Header.Version)
	}
	return nil
}

// Addr is encoded as 16 bit port (little endian) and 16 byte IPv6 address.
type CReq struct {
	Header
	Addr net.UDPAddr
}

func (*CReq) Type() byte { return PID_Puncher_CReq }

// Fails if Addr is not set
func (p CReq) MarshalBinary() ([]byte, error) {
	var b bytes.Buffer
	p.Header.Type = p.Type()
	binary.Write(&b, binary.LittleEndian, p.Header)
	binary.Write(&b, binary.LittleEndian, uint16(p.Addr.Port))
	v6 := p.Addr.IP.To16()
	if v6 == nil {
		return nil, errors.New("cannot marshal CReq: Addr.IP nil")
	}
	binary.Write(&b, binary.LittleEndian, v6)
	return b.Bytes(), nil
}

func (p *CReq) UnmarshalBinary(buf []byte) error {
	b := bytes.NewReader(buf)
	if err := binary.Read(b, binary.LittleEndian, &p.Header); err != nil {
		return ErrInvalidMessage(err.Error())
	}
	if !p.Header.Version.Supported() {
		return ErrUnsupportedVersion(p.Header.Version)
	}
	var port uint16
	if err := binary.Read(b, binary.LittleEndian, &port); err != nil {
		return ErrInvalidMessage(err.Error())
	}
	var ip [16]byte
	if err := binary.Read(b, binary.LittleEndian, &ip); err != nil {
		return ErrInvalidMessage(err.Error())
	}
	p.Addr = net.UDPAddr{Port: int(port), IP: ip[:]}
	return nil
}
