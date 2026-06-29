package cmd

import (
	"net"
	"testing"
)

func TestPortInUse(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	if !portInUse(port) {
		t.Errorf("portInUse(%d) = false while a listener is active, want true", port)
	}

	ln.Close()
	if portInUse(port) {
		t.Errorf("portInUse(%d) = true after the listener closed, want false", port)
	}
}
