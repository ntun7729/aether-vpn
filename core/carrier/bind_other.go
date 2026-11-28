//go:build !linux

package carrier

import "net"

func setDialerInterface(d *net.Dialer, iface string) {}
