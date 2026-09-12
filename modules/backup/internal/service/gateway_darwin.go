package service

import "net"

// macOS is development-only. Never trust gateway impersonation there.
func rootUnixPeer(net.Conn) bool { return false }
