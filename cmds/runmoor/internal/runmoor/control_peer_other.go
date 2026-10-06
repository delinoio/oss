//go:build !darwin && !linux

package runmoor

import "net"

func controlPeerPID(net.Conn) (int, error) { return 0, unsupported() }
