//go:build !windows

package otoha

import (
	"context"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

func dialDesktop(ctx context.Context, path string) (net.Conn, error) {
	s, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if s.Mode()&os.ModeSocket == 0 || s.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("private Desktop socket required")
	}
	if st, ok := s.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return nil, fmt.Errorf("Desktop socket belongs to another user")
	}
	return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", path)
}
