package service

import (
	"golang.org/x/sys/unix"
	"net"
)

func rootUnixPeer(conn net.Conn) bool {
	c, ok := conn.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := c.SyscallConn()
	if err != nil {
		return false
	}
	var root bool
	err = raw.Control(func(fd uintptr) {
		cred, e := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		root = e == nil && cred.Uid == 0
	})
	return err == nil && root
}
