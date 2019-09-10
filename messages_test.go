package netpuncher

import (
	"bytes"
	"net"
	"reflect"
	"testing"
)

const version = 1

var samplePackets = []PuncherPacket{
	&IDReq{Header{PID_Puncher_IDReq, version}},
	&AssID{Header{PID_Puncher_AssID, version}, 0xf0f0f0f0},
	&SReq{Header{PID_Puncher_SReq, version}, 0xf0f0f0f0},
	&CReq{Header{PID_Puncher_CReq, version}, net.UDPAddr{Port: 0xff11, IP: net.ParseIP("2001:db8::1337")}},
	&SReqTCP{Header{PID_Puncher_SReqTCP, version}, 0xf1f1f1f1},
	&CReqTCP{Header{PID_Puncher_CReqTCP, version}, net.TCPAddr{Port: 0xff11, IP: net.ParseIP("2001:db8::1337")}, net.TCPAddr{Port: 0xff22, IP: net.ParseIP("2001:db8::1338")}},
}

func TestMarshalRoundtrip(t *testing.T) {
	for _, pkt := range samplePackets {
		buf, err := pkt.MarshalBinary()
		if err != nil {
			t.Errorf("%T.MarshalBinary() failed: %v", pkt, err)
			continue
		}
		cpy := reflect.New(reflect.Indirect(reflect.ValueOf(pkt)).Type()).Interface().(PuncherPacket)
		if err = cpy.UnmarshalBinary(buf); err != nil {
			t.Errorf("%T.UnmarshalBinary() failed: %v", pkt, err)
			continue
		}
		if !reflect.DeepEqual(pkt, cpy) {
			t.Errorf("%T packets not equal: %+v != %+v", pkt, pkt, cpy)
		}
		// same with ReadFrom (might fail if MaxPacketSize is too small)
		r := bytes.NewReader(buf)
		cpy2, err := ReadFrom(r)
		if err != nil {
			t.Errorf("ReadFrom for %T failed: %v", pkt, err)
			continue
		}
		if !reflect.DeepEqual(pkt, cpy2) {
			t.Errorf("%T packets not equal after ReadFrom: %+v != %+v", pkt, pkt, cpy2)
		}
	}
}

// Test unmarshalling fake packets with an unsupported version.
func TestUnsupportedVersion(t *testing.T) {
	buf := make([]byte, 100)
	buf[1] = 0xff
	types := []byte{PID_Puncher_IDReq, PID_Puncher_AssID, PID_Puncher_SReq, PID_Puncher_CReq}
	for _, typ := range types {
		buf[0] = typ
		r := bytes.NewReader(buf)
		_, err := ReadFrom(r)
		if _, ok := err.(ErrUnsupportedVersion); !ok {
			t.Errorf("unexpected error: %v", err)
		}
	}
}
