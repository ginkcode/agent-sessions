//go:build linux

package remote

import (
	"errors"
	"net"
	"os"
	"syscall"
)

// ErrUnauthorizedPeer is returned when a client socket has a different UID than current process.
var ErrUnauthorizedPeer = errors.New("unauthorized peer UID")

func checkPeerUID(conn *net.UnixConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var sysErr error
	var peerUID int
	err = raw.Control(func(fd uintptr) {
		ucred, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if err != nil {
			sysErr = err
			return
		}
		peerUID = int(ucred.Uid)
	})
	if err != nil {
		return err
	}
	if sysErr != nil {
		return sysErr
	}
	if peerUID != os.Getuid() {
		return ErrUnauthorizedPeer
	}
	return nil
}
