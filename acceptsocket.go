package geloop

import (
	"net"
	"syscall"
	"time"
	"unsafe"
)

// AcceptSocketRequest represents the request about accepting a socket.
// The request should not be modified since be passed
// as an argument to *Loop.AcceptSocket call until be released.
type AcceptSocketRequest struct {
	// The watcher id by adopting the file descriptor of
	// a socket listened.
	WatcherID int64

	// The deadline of the request.
	Deadline time.Time

	// The function called when there is a socket accepted.
	//
	// @param request
	//     The request bound to.
	//
	// @param err
	//     ErrClosed - when the loop has been closed;
	//     ErrInvalidWatcherID - when the watcher id is invalid.
	//     ErrFdClosed - when the fd has been closed during
	//                   the request process;
	//     ErrRequestCanceled - when the request has been
	//                          canceled;
	//     ErrDeadlineReached - when the deadline has been
	//                          reached;
	//     the other errors the syscall.Accept4() returned.
	//
	// @param newFd
	//     The file descriptors of the socket accepted.
	Callback func(request *AcceptSocketRequest, err error, newFd int)

	// The optional function called when the request is released.
	//
	// @param request
	//     The request bound to.
	Cleanup func(request *AcceptSocketRequest)

	r request
}

// AcceptSocket requests to accept a socket.
// It returns the request id for cancellation.
func (l *Loop) AcceptSocket(request1 *AcceptSocketRequest) int64 {
	request1.r.OnTask = func(r *request) bool {
		r1 := getAcceptSocketRequest(r)

		if r1.process() {
			return true
		}

		if !r1.Deadline.IsZero() {
			r1.r.AddAlarm(r1.Deadline)
		}

		return false
	}

	request1.r.OnWatch = func(r *request) bool {
		r1 := getAcceptSocketRequest(r)
		return r1.process()
	}

	request1.r.OnAlarm = func(r *request) bool {
		r1 := getAcceptSocketRequest(r)
		r1.Callback(r1, ErrDeadlineReached, -1)
		return true
	}

	request1.r.OnError = func(r *request, err error) {
		r1 := getAcceptSocketRequest(r)
		r1.Callback(r1, err, -1)
	}

	request1.r.OnCleanup = func(r *request) {
		r1 := getAcceptSocketRequest(r)

		if f := r1.Cleanup; f != nil {
			f(r1)
		}
	}

	return request1.r.Submit(l)
}

func (r *AcceptSocketRequest) process() bool {
	fd, err := r.r.Loop().getFd(r.WatcherID)

	if err != nil {
		r.Callback(r, err, -1)
		return true
	}

	for {
		newFd, _, err := syscall.Accept4(fd, syscall.SOCK_NONBLOCK|syscall.SOCK_CLOEXEC)

		if err != nil {
			switch err {
			case syscall.EINTR, syscall.ECONNABORTED:
				continue
			case syscall.EAGAIN:
				r.r.AddReadableWatch(r.WatcherID)
				return false
			default:
				r.Callback(r, err, -1)
				return true
			}
		}

		r.Callback(r, nil, newFd)
		return true
	}
}

// Listen returns the file descriptor of the socket listened
// on the given network and address.
func Listen(network, address string) (int, error) {
	listener, err := net.Listen(network, address)

	if err != nil {
		return -1, err
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
		return -1, err
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
		return -1, err2
	}

	listener.Close()

	if err != nil {
		return -1, err
	}

	return listenFd, nil
}

func getAcceptSocketRequest(r *request) *AcceptSocketRequest {
	return (*AcceptSocketRequest)(unsafe.Pointer(uintptr(unsafe.Pointer(r)) - unsafe.Offsetof(AcceptSocketRequest{}.r)))
}
