package geloop

import (
	"net"
	"os"
	"syscall"
	"unsafe"

	"github.com/roy2220/geloop/internal/poller"
)

// AcceptSocketsRequest represents the request about accepting sockets.
// The request should not be modified since be passed
// as an argument to *Loop.AcceptSockets call until be released.
type AcceptSocketsRequest struct {
	// The network name.
	NetworkName string

	// The address listened on.
	ListenAddress string

	// The function called when data is received.
	//
	// @param request
	//     The request bound to.
	//
	// @param err
	//     ErrClosed - when the loop is closed;
	//     ErrRequestCanceled - when the request is canceled;
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

	r                      request
	listenerFile           *os.File
	listenerFD             int
	listenerFileIsAttached bool
}

// AcceptSockets requests to accept sockets to attach to the loop.
// It returns the request id for cancellation.
func (l *Loop) AcceptSockets(request1 *AcceptSocketsRequest) (uint64, error) {
	listener, err := net.Listen(request1.NetworkName, request1.ListenAddress)

	if err != nil {
		return 0, err
	}

	var listenerFile *os.File

	switch listener := listener.(type) {
	case *net.TCPListener:
		listenerFile, err = listener.File()
	case *net.UnixListener:
		listenerFile, err = listener.File()
	default:
		panic("unreachable code")
	}

	listener.Close()

	if err != nil {
		return 0, err
	}

	listenerFD := int(listenerFile.Fd())

	if err := syscall.SetNonblock(listenerFD, true); err != nil {
		listenerFile.Close()
		return 0, err
	}

	request1.listenerFile = listenerFile
	request1.listenerFD = listenerFD

	request1.r.OnTask = func(r *request) bool {
		r1 := getAcceptSocketsRequest(r)
		l := r1.r.Loop()
		l.attachFile(r1.listenerFD)
		r1.listenerFileIsAttached = true
		l.addWatch(&r1.r, r1.listenerFD, poller.EventReadable)
		return false
	}

	request1.r.OnWatch = func(r *request) bool {
		r1 := getAcceptSocketsRequest(r)
		l := r1.r.Loop()

		for {
			fd, _, err := syscall.Accept4(r1.listenerFD, syscall.SOCK_NONBLOCK|syscall.SOCK_CLOEXEC)

			if err != nil {
				switch err {
				case syscall.EINTR, syscall.ECONNABORTED:
					continue
				case syscall.EAGAIN:
					l.addWatch(&r1.r, r1.listenerFD, poller.EventReadable)
					return false
				default:
					r1.Callback(r1, err, -1)
					return true
				}
			}

			l.attachFile(fd)
			r1.Callback(r1, nil, fd)
		}
	}

	request1.r.OnError = func(r *request, err error) {
		r1 := getAcceptSocketsRequest(r)
		r1.Callback(r1, err, -1)
	}

	request1.r.OnCleanup = func(r *request) {
		r1 := getAcceptSocketsRequest(r)

		if r1.listenerFileIsAttached {
			r1.r.Loop().detachFile(r1.listenerFD)
			r1.listenerFileIsAttached = false
		}

		r1.listenerFile.Close()
		r1.listenerFile = nil

		if f := r1.Cleanup; f != nil {
			f(r1)
		}
	}

	return l.preAddRequest(&request1.r), nil
}

func getAcceptSocketsRequest(r *request) *AcceptSocketsRequest {
	return (*AcceptSocketsRequest)(unsafe.Pointer(uintptr(unsafe.Pointer(r)) - unsafe.Offsetof(AcceptSocketsRequest{}.r)))
}
