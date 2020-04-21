package geloop

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// Server represents a server.
type Server struct {
	URL          string
	OnError      func(s *Server, err error)
	OnSetup      func(s *Server)
	OnConnection func(s *Server, newFd int)
	OnShutdown   func(s *Server)
	OnCleanup    func(s *Server)

	loop                   *Loop
	newFdPreparers         []newFdPreparer
	isAdded                int32
	watcherID              int64
	acceptSocketRequest    AcceptSocketRequest
	acceptSocketRequestErr error
}

// Init initializes the server with the given loop
// and then returns the server.
func (s *Server) Init(loop *Loop) *Server {
	s.loop = loop
	return s
}

// Add adds the server to loop.
func (s *Server) Add() (returnedErr error) {
	url, err := url.Parse(s.URL)

	if err != nil {
		return err
	}

	fd, err := Listen(url.Scheme, url.Host)

	if err != nil {
		return err
	}

	defer func() {
		if returnedErr != nil {
			syscall.Close(fd)
		}
	}()

	s.newFdPreparers, err = makeNewFdPreparers(url)

	if err != nil {
		return err
	}

	if atomic.SwapInt32(&s.isAdded, 1) == 1 {
		panic(errors.New("geloop: server already added"))
	}

	s.loop.AdoptFd(&AdoptFdRequest{
		Fd:             fd,
		CloseFdOnError: true,

		Callback: func(request *AdoptFdRequest, err error, watcherID int64) {
			if err != nil {
				s.handleError(err)
				return
			}

			s.handleSetup(watcherID)
		},
	})

	return nil
}

// Remove removes the server from loop.
func (s *Server) Remove() {
	s.loop.DoWork(&DoWorkRequest{
		Callback: func(request *DoWorkRequest, err error) {
			if err != nil {
				return
			}

			s.loop.CloseFd(&CloseFdRequest{
				WatcherID: s.watcherID,
			})
		},
	})
}

func (s *Server) handleError(err error) {
	if f := s.OnError; f != nil {
		f(s, err)
	}

	if !s.isSetUp() {
		s.handleCleanup()
		return
	}

	s.handleShutdown(true)
}

func (s *Server) handleSetup(watcherID int64) {
	s.watcherID = watcherID

	if f := s.OnSetup; f != nil {
		f(s)
	}

	s.acceptSocketRequest = AcceptSocketRequest{
		WatcherID: watcherID,

		Callback: func(request *AcceptSocketRequest, err error, newFd int) {
			s := getServer(request)

			if err != nil {
				s.acceptSocketRequestErr = err
				return
			}

			for _, newFdPreparer := range s.newFdPreparers {
				if err = newFdPreparer(newFd); err != nil {
					syscall.Close(newFd)
					s.acceptSocketRequestErr = err
					return
				}
			}

			s.handleConnection(newFd)
		},

		Cleanup: func(request *AcceptSocketRequest) {
			s := getServer(request)

			if err := s.acceptSocketRequestErr; err != nil {
				switch err {
				case ErrClosed, ErrInvalidWatcherID, ErrFdClosed:
					s.handleShutdown(false)
				default:
					s.handleError(err)
				}

				return
			}

			s.loop.AcceptSocket(request)
		},
	}

	s.loop.AcceptSocket(&s.acceptSocketRequest)
}

func (s *Server) handleConnection(newFd int) {
	s.OnConnection(s, newFd)
}

func (s *Server) handleShutdown(closeFd bool) {
	watcherID := s.watcherID
	s.watcherID = 0

	if f := s.OnShutdown; f != nil {
		f(s)
	}

	if !closeFd {
		s.handleCleanup()
		return
	}

	s.loop.CloseFd(&CloseFdRequest{
		WatcherID: watcherID,

		Cleanup: func(*CloseFdRequest) {
			s.handleCleanup()
		},
	})
}

func (s *Server) handleCleanup() {
	atomic.StoreInt32(&s.isAdded, 0)

	if f := s.OnCleanup; f != nil {
		f(s)
	}
}

func (s *Server) isSetUp() bool {
	return s.watcherID >= 1
}

type newFdPreparer func(newFd int) (err error)

func getServer(acceptSocketRequest *AcceptSocketRequest) *Server {
	return (*Server)(unsafe.Pointer(uintptr(unsafe.Pointer(acceptSocketRequest)) - unsafe.Offsetof(Server{}.acceptSocketRequest)))
}

func makeNewFdPreparers(serverURL *url.URL) ([]newFdPreparer, error) {
	newFdPreparers := []newFdPreparer(nil)

	switch serverURL.Scheme {
	case "tcp", "tcp4", "tcp6":
		{
			var noDelay bool

			if param := serverURL.Query().Get("nodelay"); param == "" {
				noDelay = defaultTCPNoDelay
			} else {
				switch strings.ToLower(param) {
				case "true":
					noDelay = true
				case "false":
					noDelay = false
				default:
					return nil, fmt.Errorf("geloop: invalid param: nodelay=%q", param)
				}
			}

			newFdPreparers = append(newFdPreparers, func(newFd int) error {
				return tcpSetNoDelay(newFd, noDelay)
			})
		}

		{
			var keepAlive time.Duration

			if param := serverURL.Query().Get("keepalive"); param == "" {
				keepAlive = defaultTCPKeepAlive
			} else {
				var err error
				keepAlive, err = time.ParseDuration(param)

				if err != nil {
					return nil, fmt.Errorf("geloop: invalid param: keepalive=%q", param)
				}
			}

			newFdPreparers = append(newFdPreparers, func(newFd int) error {
				return tcpSetKeepAlive(newFd, keepAlive)
			})
		}
	}

	return newFdPreparers, nil
}

const defaultTCPNoDelay = true

func tcpSetNoDelay(fd int, noDelay bool) error {
	var onOff int

	if noDelay {
		onOff = 1
	} else {
		onOff = 0
	}

	return syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, syscall.TCP_NODELAY, onOff)
}

const defaultTCPKeepAlive = 300 * time.Second

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
