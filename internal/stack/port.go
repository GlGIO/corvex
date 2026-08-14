package stack

import (
	"fmt"
	"net"
)

// PortInUse reports whether something is already listening on the TCP port.
// It tries to bind briefly; if the bind fails the port is taken.
func PortInUse(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return true
	}
	ln.Close()
	return false
}
