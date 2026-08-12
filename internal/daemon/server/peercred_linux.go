package server

import (
	"fmt"
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// peerCredentialListener forces the kernel to provide credentials for every
// accepted Unix-socket peer. Filesystem ownership/mode remains the
// authorization policy; SO_PEERCRED prevents non-Unix transports or peers
// without a kernel identity from reaching gRPC.
type peerCredentialListener struct{ net.Listener }

func (l peerCredentialListener) Accept() (net.Conn, error) {
	for {
		connection, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if _, err := unixPeerCredentials(connection); err != nil {
			_ = connection.Close()
			continue
		}
		return connection, nil
	}
}

func unixPeerCredentials(connection net.Conn) (*unix.Ucred, error) {
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		return nil, fmt.Errorf("peer is not a Unix connection")
	}
	raw, err := unixConnection.SyscallConn()
	if err != nil {
		return nil, err
	}
	var credentials *unix.Ucred
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, socketErr = unix.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return nil, err
	}
	if socketErr != nil {
		return nil, socketErr
	}
	if credentials == nil || credentials.Pid <= 0 {
		return nil, fmt.Errorf("Unix peer credentials are unavailable")
	}
	return credentials, nil
}
