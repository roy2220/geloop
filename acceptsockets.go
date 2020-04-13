package geloop

import (
	"net"
	"syscall"
	"unsafe"
)

// AcceptSocketsRequest represents the request about accepting sockets.
// The request should not be modified since be passed
// as an argument to *Loop.AcceptSockets call until be released.
type AcceptSocketsRequest struct {
	// The network name.
	NetworkName string

	// The address listened on.
	ListenAddress string

	// Adopt the file descriptors of sockets accepted to the current loop.
	AdoptFds bool

	// The function called when there is a socket accepted.
	//
	// @param request
	//     The request bound to.
	//
	// @param err
	//     ErrClosed - when the loop has been closed;
	//     ErrRequestCanceled - when the request has been
	//                          canceled;
	//     the other errors the syscall.Accept4() returned.
	//
	// @param fd
	//     The file descriptor of the socket accepted.
	Callback func(request *AcceptSocketsRequest, err error, fd int)

	// The optional function called when the request is released.
	//
	// @param request
	//     The request bound to.
	Cleanup func(request *AcceptSocketsRequest)

	r                 request
	listenFd          int
	listenFdIsAdopted bool
}

// AcceptSockets requests to accept sockets.
// It returns the request id for cancellation.
func (l *Loop) AcceptSockets(request1 *AcceptSocketsRequest) (uint64, error) {
	listenFd, err := doListen(request1.NetworkName, request1.ListenAddress)

	if err != nil {
		return 0, err
	}

	request1.listenFd = listenFd

	request1.r.OnTask = func(r *request) bool {
		r1 := getAcceptSocketsRequest(r)
		r1.r.Loop().adoptFd(r1.listenFd)
		r1.listenFdIsAdopted = true
		r1.r.AddReadableWatch(r1.listenFd)
		return false
	}

	request1.r.OnWatch = func(r *request) bool {
		r1 := getAcceptSocketsRequest(r)

		for {
			fd, _, err := syscall.Accept4(r1.listenFd, syscall.SOCK_NONBLOCK|syscall.SOCK_CLOEXEC)

			if err != nil {
				switch err {
				case syscall.EINTR, syscall.ECONNABORTED:
					continue
				case syscall.EAGAIN:
					r1.r.AddReadableWatch(r1.listenFd)
					return false
				default:
					r1.Callback(r1, err, -1)
					return true
				}
			}

			if r1.AdoptFds {
				r1.r.Loop().adoptFd(fd)
			}

			r1.Callback(r1, nil, fd)
		}
	}

	request1.r.OnError = func(r *request, err error) {
		r1 := getAcceptSocketsRequest(r)
		r1.Callback(r1, err, -1)
	}

	request1.r.OnCleanup = func(r *request) {
		r1 := getAcceptSocketsRequest(r)

		if r1.listenFdIsAdopted {
			r.Loop().closeFd(r1.listenFd)
			r1.listenFdIsAdopted = false
		}

		r1.listenFd = 0

		if f := r1.Cleanup; f != nil {
			f(r1)
		}
	}

	return request1.r.Process(l), nil
}

func getAcceptSocketsRequest(r *request) *AcceptSocketsRequest {
	return (*AcceptSocketsRequest)(unsafe.Pointer(uintptr(unsafe.Pointer(r)) - unsafe.Offsetof(AcceptSocketsRequest{}.r)))
}

func doListen(networkName, listenAddress string) (int, error) {
	listener, err := net.Listen(networkName, listenAddress)

	if err != nil {
		return 0, err
	}

	var syscallConn syscall.RawConn

	switch listener := listener.(type) {
	case *net.TCPListener:
		syscallConn, err = listener.SyscallConn()
	case *net.UnixListener:
		syscallConn, err = listener.SyscallConn()
	default:
		panic("unreachable code")
	}

	if err != nil {
		return 0, err
	}

	var listenFd int

	if err2 := syscallConn.Control(func(fd uintptr) {
		syscall.ForkLock.RLock()
		defer syscall.ForkLock.RUnlock()
		listenFd, err = syscall.Dup(int(fd))

		if err != nil {
			return
		}

		syscall.CloseOnExec(listenFd)
	}); err2 != nil {
		return 0, err2
	}

	listener.Close()

	if err != nil {
		return 0, err
	}

	if err := syscall.SetNonblock(listenFd, true); err != nil {
		syscall.Close(listenFd)
		return 0, err
	}

	return listenFd, nil
}
