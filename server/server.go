package server

import (
	"fmt"
	"math/rand"
	"net"
	"time"

	"github.com/openclonk/netpuncher"
	"github.com/openclonk/netpuncher/c4netioudp"
)

type Conn struct {
	ID        uint32
	NetIOConn *c4netioudp.Conn
	version   netpuncher.ProtocolVersion
	s         *Server
}

func (c *Conn) npHeader() netpuncher.Header {
	return netpuncher.Header{Version: c.version}
}

func (c *Conn) handlePackets(req chan<- punchReq, close chan<- uint32) {
	for {
		msg, err := netpuncher.ReadFrom(c.NetIOConn)
		select {
		case <-c.s.exitch:
			return
		default:
		}
		switch errt := err.(type) {
		case c4netioudp.ErrConnectionClosed:
			if c.s.CloseConn != nil {
				c.s.CloseConn(c, &errt)
			}
			c.NetIOConn.Close()
			close <- c.ID
			return
		case netpuncher.ErrUnsupportedVersion:
			if c.s.UnsupportedVersionErr != nil {
				c.s.UnsupportedVersionErr(c, &errt)
			}
			c.NetIOConn.Close()
			continue
		case nil: // ok
		default:
			if c.s.InvalidPacketErr != nil {
				c.s.InvalidPacketErr(c, err)
			}
			continue
		}
		switch np := msg.(type) {
		case *netpuncher.IDReq:
			c.version = np.Header.Version
			buf, err := netpuncher.AssID{Header: c.npHeader(), CID: c.ID}.MarshalBinary()
			if err != nil {
				if c.s.MarshalErr != nil {
					c.s.MarshalErr(fmt.Errorf("AssID.MarshalBinary(): %v", err))
				}
				continue
			}
			c.NetIOConn.Write(buf)
			if c.s.RegisterHost != nil {
				c.s.RegisterHost(c)
			}
		case *netpuncher.SReq:
			c.version = np.Header.Version
			req <- punchReq{np.CID, c, false}
		case *netpuncher.SReqTCP:
			c.version = np.Header.Version
			req <- punchReq{np.CID, c, true}
		}
	}
}

type punchReq struct {
	id   uint32
	conn *Conn
	tcp  bool
}

type Server struct {
	AcceptConn            func(c *Conn, err error)                             // called when the server accepts a connection
	MarshalErr            func(err error)                                      // called when an error occurs during marshalling
	UnsupportedVersionErr func(c *Conn, err *netpuncher.ErrUnsupportedVersion) // called when a client sends a packet with an unsupported version
	InvalidPacketErr      func(c *Conn, err error)                             // called when a client sends an invalid packet
	RegisterHost          func(host *Conn)                                     // called when a host requests an ID
	CReq                  func(host *Conn, client *Conn)                       // called when initiating punch between host and client
	CloseConn             func(c *Conn, err *c4netioudp.ErrConnectionClosed)   // called when closing a connection

	listener *c4netioudp.Listener
	exitch   chan struct{} // signals that the server should exit
}

// randomPort generates a random dynamic port.
func randomPort(rng *rand.Rand) int {
	min := 49152
	max := 65535
	return min + rng.Intn(max-min)
}

// Listen starts the netpuncher server.
func (s *Server) Listen(network string, listenaddr *net.UDPAddr) error {
	listener, err := c4netioudp.Listen(network, listenaddr)
	if err != nil {
		return fmt.Errorf("couldn't ListenUDP: %v", err)
	}
	s.listener = listener
	s.exitch = make(chan struct{})

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	go func() {
		connch := make(chan *c4netioudp.Conn)
		conns := make(map[uint32]*Conn)
		req := make(chan punchReq)
		closech := make(chan uint32)
		go func() {
			for {
				conn, err := listener.AcceptConn()
				select {
				case <-s.exitch:
					return
				default:
				}
				if err != nil {
					if s.AcceptConn != nil {
						s.AcceptConn(nil, err)
					}
					continue
				}
				connch <- conn
			}
		}()
		for {
			select {
			case conn := <-connch:
				id := rng.Uint32()
				c := &Conn{ID: id, NetIOConn: conn, s: s}
				conns[id] = c
				go c.handlePackets(req, closech)
				if s.AcceptConn != nil {
					s.AcceptConn(c, nil)
				}
			case r := <-req:
				// The client (r.conn) requests punching from the host (r.id). We will send a
				// CReq message to both parties.
				client := r.conn
				if host, ok := conns[r.id]; ok {
					caddr := client.NetIOConn.RemoteAddr().(*net.UDPAddr)
					haddr := host.NetIOConn.RemoteAddr().(*net.UDPAddr)
					var hbuf, cbuf []byte
					var herr, cerr error
					if r.tcp {
						caddrtcp := net.TCPAddr{IP: caddr.IP, Port: randomPort(rng)}
						haddrtcp := net.TCPAddr{IP: haddr.IP, Port: randomPort(rng)}
						hbuf, herr = netpuncher.CReqTCP{
							Header:     host.npHeader(),
							SourceAddr: haddrtcp,
							DestAddr:   caddrtcp}.MarshalBinary()
						cbuf, cerr = netpuncher.CReqTCP{
							Header:     client.npHeader(),
							SourceAddr: caddrtcp,
							DestAddr:   haddrtcp}.MarshalBinary()
					} else {
						hbuf, herr = netpuncher.CReq{Header: host.npHeader(), Addr: *caddr}.MarshalBinary()
						cbuf, cerr = netpuncher.CReq{Header: client.npHeader(), Addr: *haddr}.MarshalBinary()
					}
					if herr != nil {
						if s.MarshalErr != nil {
							s.MarshalErr(fmt.Errorf("CReq.MarshalBinary() host: %v", herr))
						}
						continue
					}
					host.NetIOConn.Write(hbuf)
					if cerr != nil {
						if s.MarshalErr != nil {
							s.MarshalErr(fmt.Errorf("CReq.MarshalBinary() client: %v", cerr))
						}
						continue
					}
					client.NetIOConn.Write(cbuf)
					if s.CReq != nil {
						s.CReq(host, client)
					}
				}
			case id := <-closech:
				delete(conns, id)
			case <-s.exitch:
				return
			}
		}
	}()

	return nil
}

// Addr returns the netpuncher's local UDP address.
func (s *Server) Addr() net.Addr {
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// Close makes the netpuncher exit.
func (s *Server) Close() error {
	close(s.exitch)
	return nil
}
