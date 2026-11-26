//go:build linux

package httpapi

import (
	"net"
	"syscall"
)

func peerOwner(connection net.Conn) (int, int, bool) {
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		return 0, 0, false
	}
	raw, err := unixConnection.SyscallConn()
	if err != nil {
		return 0, 0, false
	}
	uid, gid := 0, 0
	var credentialErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if err != nil {
			credentialErr = err
			return
		}
		uid, gid = int(credentials.Uid), int(credentials.Gid)
	}); err != nil || credentialErr != nil {
		return 0, 0, false
	}
	return uid, gid, true
}
