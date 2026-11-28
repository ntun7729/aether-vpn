//go:build linux

package carrier

import (
	"net"
	"syscall"
)

func setDialerInterface(d *net.Dialer, iface string) {
	if iface == "" {
		return
	}
	d.Control = func(network, address string, c syscall.RawConn) error {
		var sockErr error
		if err := c.Control(func(fd uintptr) {
			sockErr = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, iface)
		}); err != nil {
			return err
		}
		return sockErr
	}
}
