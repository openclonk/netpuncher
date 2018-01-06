package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"

	"github.com/lluchs/netpuncher"
	"github.com/lluchs/netpuncher/c4netioudp"
	"github.com/lluchs/netpuncher/server"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	connectionCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "netpuncher_connections_total",
		Help: "Number of connections to the netpuncher",
	}, []string{"protocol"})
	disconnectCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "netpuncher_disconnects_total",
		Help: "Number of disconnects from the netpuncher",
	}, []string{"protocol"})
	hostCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "netpuncher_hosts_total",
		Help: "Number of hosts which registered with the netpuncher",
	}, []string{"protocol"})
	creqCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "netpuncher_creq_total",
		Help: "Number of CReq messages processed by the netpuncher",
	}, []string{"protocol"})
	errorCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "netpuncher_errors_total",
		Help: "Number of non-fatal errors during packet handling",
	}, []string{"protocol", "reason"})
)

func init() {
	prometheus.MustRegister(connectionCounter)
	prometheus.MustRegister(disconnectCounter)
	prometheus.MustRegister(hostCounter)
	prometheus.MustRegister(creqCounter)
	prometheus.MustRegister(errorCounter)
}

func protocol(addr net.Addr) string {
	if udpaddr, ok := addr.(*net.UDPAddr); ok {
		if udpaddr.IP.To4() != nil {
			return "IPv4"
		} else {
			return "IPv6"
		}
	}
	return "unknown"
}

func main() {
	listenaddr := net.UDPAddr{IP: net.IPv6unspecified, Port: 11115}
	if p, err := strconv.Atoi(os.Getenv("PORT")); err == nil {
		listenaddr.Port = p
	}

	server := server.Server{
		AcceptConn: func(c *server.Conn, err error) {
			if err != nil {
				log.Fatal("error during Accept: ", err)
			}
			addr := c.NetIOConn.RemoteAddr()
			log.Printf("connect: %v #%d\n", addr, c.ID)
			connectionCounter.With(prometheus.Labels{"protocol": protocol(addr)}).Inc()
		},
		MarshalErr: func(err error) {
			log.Println(err)
			errorCounter.With(prometheus.Labels{"reason": "marshal"}).Inc()
		},
		UnsupportedVersionErr: func(c *server.Conn, err *netpuncher.ErrUnsupportedVersion) {
			log.Printf("client #%d: unsupported version %d", c.ID, err)
			errorCounter.With(prometheus.Labels{"protocol": protocol(c.NetIOConn.RemoteAddr()), "reason": "unsupported version"}).Inc()
		},
		InvalidPacketErr: func(c *server.Conn, err error) {
			log.Printf("client #%d: couldn't read packet: %v", c.ID, err)
			errorCounter.With(prometheus.Labels{"protocol": protocol(c.NetIOConn.RemoteAddr()), "reason": "invalid packet"}).Inc()
		},
		RegisterHost: func(host *server.Conn) {
			log.Printf("host: #%d", host.ID)
			hostCounter.With(prometheus.Labels{"protocol": protocol(host.NetIOConn.RemoteAddr())}).Inc()
		},
		CReq: func(host *server.Conn, client *server.Conn) {
			clientaddr := client.NetIOConn.RemoteAddr()
			log.Printf("CReq: client %v <--> host %v #%d\n", clientaddr, host.NetIOConn.RemoteAddr(), host.ID)
			// The client sends the CReq message, so label this by the client's
			// protocol. In the usual case, the two protocols will be the same
			// anyways.
			creqCounter.With(prometheus.Labels{"protocol": protocol(clientaddr)}).Inc()
		},
		CloseConn: func(c *server.Conn, err *c4netioudp.ErrConnectionClosed) {
			addr := c.NetIOConn.RemoteAddr()
			log.Printf("close:   %v #%d (%s)\n", addr, c.ID, err)
			disconnectCounter.With(prometheus.Labels{"protocol": protocol(addr)}).Inc()
		},
	}

	err := server.Listen("udp", &listenaddr)
	if err != nil {
		log.Fatal("couldn't ListenUDP", err)
	}
	defer server.Close()
	log.Printf("netpuncher listening on %v", server.Addr())

	if addr := os.Getenv("METRICS_ADDR"); addr != "" {
		log.Printf("metrics listening on %s", addr)
		http.Handle("/metrics", promhttp.Handler())
		go log.Fatal(http.ListenAndServe(addr, nil))
	}

	// Wait for an interrupt. Without this special handling, the connection
	// would not be closed properly.
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
}
