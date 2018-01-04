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
	connectionCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "netpuncher_connections_total",
		Help: "Number of connections to the netpuncher",
	})
	currentConnectionsGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "netpuncher_connections",
		Help: "Number of clients currently connected to the netpuncher",
	})
	hostCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "netpuncher_hosts_total",
		Help: "Number of hosts which registered with the netpuncher",
	})
	creqCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "netpuncher_creq_total",
		Help: "Number of CReq messages processed by the netpuncher",
	})
	errorCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "netpuncher_errors_total",
		Help: "Number of non-fatal errors during packet handling",
	}, []string{"reason"})
)

func init() {
	prometheus.MustRegister(connectionCounter)
	prometheus.MustRegister(currentConnectionsGauge)
	prometheus.MustRegister(hostCounter)
	prometheus.MustRegister(creqCounter)
	prometheus.MustRegister(errorCounter)
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
			log.Printf("connect: %v #%d\n", c.NetIOConn.RemoteAddr(), c.ID)
			connectionCounter.Inc()
			currentConnectionsGauge.Inc()
		},
		MarshalErr: func(err error) {
			log.Println(err)
			errorCounter.With(prometheus.Labels{"reason": "marshal"}).Inc()
		},
		UnsupportedVersionErr: func(c *server.Conn, err *netpuncher.ErrUnsupportedVersion) {
			log.Printf("client #%d: unsupported version %d", c.ID, err)
			errorCounter.With(prometheus.Labels{"reason": "unsupported version"}).Inc()
		},
		InvalidPacketErr: func(c *server.Conn, err error) {
			log.Printf("client #%d: couldn't read packet: %v", c.ID, err)
			errorCounter.With(prometheus.Labels{"reason": "invalid packet"}).Inc()
		},
		RegisterHost: func(host *server.Conn) {
			log.Printf("host: #%d", host.ID)
			hostCounter.Inc()
		},
		CReq: func(host *server.Conn, client *server.Conn) {
			log.Printf("CReq: client %v <--> host %v #%d\n", client.NetIOConn.RemoteAddr(), host.NetIOConn.RemoteAddr(), host.ID)
			creqCounter.Inc()
		},
		CloseConn: func(c *server.Conn, err *c4netioudp.ErrConnectionClosed) {
			log.Printf("close:   %v #%d (%s)\n", c.NetIOConn.RemoteAddr(), c.ID, err)
			currentConnectionsGauge.Dec()
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
