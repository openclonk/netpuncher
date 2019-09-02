GO ?= go

all: netpuncher-server netpuncher-client

netpuncher-server:
	$(GO) build github.com/openclonk/netpuncher/cmd/netpuncher-server

netpuncher-client:
	$(GO) build github.com/openclonk/netpuncher/cmd/netpuncher-client

test:
	$(GO) test ./...

# The Go tool already skips unnecessary work
.PHONY: netpuncher-server netpuncher-client test
