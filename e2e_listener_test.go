package main

import "net"

// newListener is split into its own file so pickFreeAddr in e2e_test.go can
// stay clean of net imports it doesn't otherwise need.
func newListener() (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}
