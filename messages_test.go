package netpuncher

import "testing"

var addrRegexpTestCases = []struct {
	addr, ip, port string
}{
	{"\x00[::ffff:46.5.2.87]:30746\x00", "[::ffff:46.5.2.87]", "30746"},
	{"\x00[2a02:8071:2b8e:a500:784e:2394:9ac2:c022]:39457\x00", "[2a02:8071:2b8e:a500:784e:2394:9ac2:c022]", "39457"},
	{"\x0046.5.2.87:30746\x00", "46.5.2.87", "30746"},
}

func TestAddrRegexp(t *testing.T) {
	addr := []byte("\x00[::ffff:46.5.2.87]:30746\x00")
	for _, tc := range addrRegexpTestCases {
		m := addrRegexp.FindStringSubmatch(tc.addr)
		if m == nil {
			t.Fatalf("addrRegexp did not match %q", addr)
		}
		if m[1] != tc.ip {
			t.Fatalf("%q != %q", m[1], tc.ip)
		}
		if m[2] != tc.port {
			t.Fatalf("%q != %q", m[2], tc.port)
		}
	}
}
