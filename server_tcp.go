package geloop

import (
	"fmt"
	"net/url"
	"strings"
	"syscall"
	"time"
)

const (
	tcpDefaultNoDelay   = true
	tcpDefaultKeepAlive = 300 * time.Second
)

func tcpMakeNewFdPreparers(params url.Values) ([]newFdPreparer, error) {
	newFdPreparers := []newFdPreparer(nil)

	{
		var noDelay bool

		if param := params.Get("nodelay"); param == "" {
			noDelay = tcpDefaultNoDelay
		} else {
			switch strings.ToLower(param) {
			case "true":
				noDelay = true
			case "false":
				noDelay = false
			default:
				return nil, fmt.Errorf("geloop: invalid tcp param: nodelay=%q", param)
			}
		}

		newFdPreparers = append(newFdPreparers, func(newFd int) error {
			return tcpSetNoDelay(newFd, noDelay)
		})
	}

	{
		var keepAlive time.Duration

		if param := params.Get("keepalive"); param == "" {
			keepAlive = tcpDefaultKeepAlive
		} else {
			var err error
			keepAlive, err = time.ParseDuration(param)

			if err != nil {
				return nil, fmt.Errorf("geloop: invalid tcp param: keepalive=%q", param)
			}
		}

		newFdPreparers = append(newFdPreparers, func(newFd int) error {
			return tcpSetKeepAlive(newFd, keepAlive)
		})
	}

	return newFdPreparers, nil
}

func tcpSetNoDelay(fd int, noDelay bool) error {
	var onOff int

	if noDelay {
		onOff = 1
	} else {
		onOff = 0
	}

	return syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, syscall.TCP_NODELAY, onOff)
}

func tcpSetKeepAlive(fd int, keepAlive time.Duration) error {
	var onOff int

	if keepAlive < 1 {
		onOff = 0
	} else {
		onOff = 1
	}

	if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_KEEPALIVE, onOff); err != nil {
		return err
	}

	if onOff == 1 {
		idle := int((keepAlive + time.Second/2) / time.Second)
		const cnt = 3
		intvl := int(float64(idle)/cnt + 0.5)

		if err := syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, syscall.TCP_KEEPIDLE, idle); err != nil {
			return err
		}

		if err := syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, syscall.TCP_KEEPINTVL, intvl); err != nil {
			return err
		}

		if err := syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, syscall.TCP_KEEPCNT, cnt); err != nil {
			return err
		}
	}

	return nil
}
