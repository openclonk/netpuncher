package c4netioudp

import "net"

// Return value from ReadFromUDP
type rfu struct {
	buf  []byte
	n    int
	addr *net.UDPAddr
	err  error
}

// udp.ReadFromUDP for goroutine use
func readFromUDP(udp *net.UDPConn, rfuchan chan<- rfu, quit <-chan bool) {
	for {
		var r rfu
		r.buf = make([]byte, 1500)
		r.n, r.addr, r.err = udp.ReadFromUDP(r.buf)
		select {
		case rfuchan <- r: // ok
		case <-quit:
			return
		}
	}
}

type writerToUDP struct {
	udp  *net.UDPConn
	addr *net.UDPAddr
}

func (w writerToUDP) Write(b []byte) (n int, err error) {
	return w.udp.WriteToUDP(b, w.addr)
}
