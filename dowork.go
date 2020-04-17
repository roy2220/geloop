package geloop

import (
	"unsafe"
)

// DoWorkRequest represents a request about doing work.
// The request should not be modified since be passed
// as an argument to *Loop.AdoptFd call until be released.
type DoWorkRequest struct {
	// The function called when the request is ready to work.
	//
	// @param request
	//     The request bound to.
	//
	// @param err
	//     ErrClosed - when the loop has been closed;
	Callback func(request *DoWorkRequest, err error)

	// The optional function called when the request is released.
	//
	// @param request
	//     The request bound to.
	Cleanup func(request *AdoptFdRequest)

	r request
}

// DoWork requests to do work.
func (l *Loop) DoWork(request1 *DoWorkRequest) {
	request1.r.OnTask = func(r *request) bool {
		r1 := getCloseFdRequest(r)
		r1.Callback(r1, nil)
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

	request1.r.Submit(l)
}

func getDoWorkRequest(r *request) *DoWorkRequest {
	return (*DoWorkRequest)(unsafe.Pointer(uintptr(unsafe.Pointer(r)) - unsafe.Offsetof(DoWorkRequest{}.r)))
}
