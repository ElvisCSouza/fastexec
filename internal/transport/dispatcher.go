package transport

import (
	"time"
)

func Dispatch(hostName string, cmd string, timeout time.Duration) (*ExecResult, error) {
	target, err := ResolveHost(hostName)
	if err != nil {
		return nil, err
	}
	if target.IsLocal {
		return ExecLocal(cmd, timeout)
	}
	return ExecDaemon(target.TCPAddr, cmd, timeout)
}
