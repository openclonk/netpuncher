GO ?= go

netpuncher-server:
	$(GO) build github.com/openclonk/netpuncher/cmd/netpuncher-server

test:
	$(GO) test ./...

# The Go tool already skips unnecessary work
.PHONY: netpuncher-server test
