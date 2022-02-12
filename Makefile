GO ?= go

all: netpuncher-server netpuncher-client c4netioudp-connect

netpuncher-server:
	$(GO) build github.com/openclonk/netpuncher/cmd/netpuncher-server

netpuncher-client:
	$(GO) build github.com/openclonk/netpuncher/cmd/netpuncher-client

c4netioudp-connect:
	$(GO) build github.com/openclonk/netpuncher/cmd/c4netioudp-connect

test:
	$(GO) test ./...

# The Go tool already skips unnecessary work
.PHONY: netpuncher-server netpuncher-client test
