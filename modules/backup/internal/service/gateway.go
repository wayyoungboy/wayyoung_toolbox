package service

import (
	"context"
	"net"
)

// GatewayConnection trusts only root peers on the private Unix listener. A
// caller-supplied HTTP header alone never grants an application session.
func GatewayConnection(ctx context.Context, conn net.Conn) context.Context {
	return context.WithValue(ctx, gatewayKey{}, rootUnixPeer(conn))
}
