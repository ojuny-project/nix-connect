//go:build !linux

package httpapi

import "net"

func peerOwner(net.Conn) (int, int, bool) {
	return 0, 0, false
}
