package geloop

import (
	"syscall"
	"unsafe"
)

// AdoptFdRequest represents a request about adopting a file descriptor.
// The request should not be modified since be passed
// as an argument to *Loop.AdoptFd call until be released.
type AdoptFdRequest struct {
	// The file descriptor to adopt. All file descriptors adopted in a
	// loop will automatically be closed when the loop is closed.
	Fd int

	// Close the file descriptor when any error occurs during
	// the request process.
	CloseFdOnError bool

	// The function called when the request is completed.
	//
	// @param request
	//     The request bound to.
	//
	// @param watcherID
	//     The watcher id to read/write file.
	//
	// @param err
	//     ErrClosed - when the loop has been closed;
	Callback func(request *AdoptFdRequest, err error, watcherID int64)

	// The optional function called when the request is released.
	//
	// @param request
	//     The request bound to.
	Cleanup func(request *AdoptFdRequest)

	r request
}

// AdoptFd requests to adopt a file descriptor to the loop.
func (l *Loop) AdoptFd(request1 *AdoptFdRequest) {
	request1.r.OnTask = func(r *request) bool {
		r1 := getAdoptFdRequest(r)
		watcherID := r1.r.Loop().adoptFd(r1.Fd)
		r1.Callback(r1, nil, watcherID)
		return true
	}

	request1.r.OnError = func(r *request, err error) {
		r1 := getAdoptFdRequest(r)

		if r1.CloseFdOnError {
			syscall.Close(r1.Fd)
		}

		r1.Callback(r1, err, 0)
	}

	request1.r.OnCleanup = func(r *request) {
		r1 := getAdoptFdRequest(r)

		if f := r1.Cleanup; f != nil {
			f(r1)
		}
	}

	request1.r.Submit(l)
}

func getAdoptFdRequest(r *request) *AdoptFdRequest {
	return (*AdoptFdRequest)(unsafe.Pointer(uintptr(unsafe.Pointer(r)) - unsafe.Offsetof(AdoptFdRequest{}.r)))
}
