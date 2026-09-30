//go:build !linux

package remote

import "net"

func checkPeerUID(conn *net.UnixConn) error {
	return nil
}
