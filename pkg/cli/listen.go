package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strings"
)

// listenLocal listens on "host:port" over TCP, or on "unix:PATH" as a unix
// socket. The socket is created 0600 since anyone who can connect speaks as
// this user's key. A stale socket left by a previous run is replaced; any
// other existing file is an error.
func listenLocal(addr string) (net.Listener, error) {
	path, ok := strings.CutPrefix(addr, "unix:")
	if !ok {
		return net.Listen("tcp", addr)
	}
	if path == "" {
		return nil, errors.New("unix: needs a socket path")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&fs.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket", path)
		}
		if c, err := net.Dial("unix", path); err == nil {
			c.Close()
			return nil, fmt.Errorf("%s is in use by another process", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	old := umask(0o177)
	l, err := net.Listen("unix", path)
	umask(old)
	if err != nil {
		return nil, err
	}
	l.(*net.UnixListener).SetUnlinkOnClose(true)
	return l, nil
}
