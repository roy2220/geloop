package geloop

import "unsafe"

// CloseFdRequest represents a request about closing a file descriptor.
// The request should not be modified since be passed
// as an argument to *Loop.CloseFd call until be released.
type CloseFdRequest struct {
	// The file descriptor to close. All file descriptors adopted in a
	// loop will automatically be closed when the loop is closed.
	Fd int

	// The optional function called when the request is completed.
	//
	// @param request
	//     The request bound to.
	//
	// @param err
	//     ErrClosed - when the loop has been closed;
	//     ErrInvalidFd - when the fd hasn't yet been adopted
	//                    or has already been closed;
	//     the other errors the syscall.Close() returned.
	Callback func(request *CloseFdRequest, err error)

	// The optional function called when the request is released.
	//
	// @param request
	//     The request bound to.
	Cleanup func(request *CloseFdRequest)

	r request
}

// CloseFd requests to close a file descriptor.
func (l *Loop) CloseFd(request1 *CloseFdRequest) {
	if request1.Callback == nil {
		request1.Callback = func(*CloseFdRequest, error) {}
	}

	request1.r.OnTask = func(r *request) bool {
		r1 := getCloseFdRequest(r)
		err := r1.r.Loop().closeFd(r1.Fd)
		r1.Callback(r1, err)
		return true
	}

	request1.r.OnError = func(r *request, err error) {
		r1 := getCloseFdRequest(r)
		r1.Callback(r1, err)
	}

	request1.r.OnCleanup = func(r *request) {
		r1 := getCloseFdRequest(r)

		if f := r1.Cleanup; f != nil {
			f(r1)
		}
	}

	request1.r.Process(l)
}

func getCloseFdRequest(r *request) *CloseFdRequest {
	return (*CloseFdRequest)(unsafe.Pointer(uintptr(unsafe.Pointer(r)) - unsafe.Offsetof(CloseFdRequest{}.r)))
}
