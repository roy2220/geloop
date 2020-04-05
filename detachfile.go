package geloop

import (
	"syscall"
	"unsafe"
)

// DetachFileRequest represents the request about detaching a file.
// The request should not be modified since be passed
// as an argument to *Loop.DetachFile call until be released.
type DetachFileRequest struct {
	// The descriptor of the file to detach.
	FD int

	// Close the file when the request is released.
	CloseFile bool

	// The optional function called when the file is detached.
	//
	// @param request
	//     The request bound to.
	//
	// @param err
	//     ErrClosed - when the loop is closed;
	//
	// @param ok
	//     The boolean value indicating whether the
	//     file was attached.
	Callback func(request *DetachFileRequest, err error, ok bool)

	// The optional function called when the request is released.
	//
	// @param request
	//     The request bound to.
	Cleanup func(request *DetachFileRequest)

	r request
}

// DetachFile requests to detach the given file from the loop.
func (l *Loop) DetachFile(request1 *DetachFileRequest) {
	if request1.Callback == nil {
		request1.Callback = func(*DetachFileRequest, error, bool) {}
	}

	request1.r.OnTask = func(r *request) bool {
		r1 := getDetachFileRequest(r)
		r1.Callback(r1, nil, r1.r.Loop().detachFile(r1.FD))
		return true
	}

	request1.r.OnError = func(r *request, err error) {
		r1 := getDetachFileRequest(r)
		r1.Callback(r1, err, false)
	}

	request1.r.OnCleanup = func(r *request) {
		r1 := getDetachFileRequest(r)

		if r1.CloseFile {
			syscall.Close(r1.FD)
		}

		if f := r1.Cleanup; f != nil {
			f(r1)
		}
	}

	l.preAddRequest(&request1.r)
}

func getDetachFileRequest(r *request) *DetachFileRequest {
	return (*DetachFileRequest)(unsafe.Pointer(uintptr(unsafe.Pointer(r)) - unsafe.Offsetof(DetachFileRequest{}.r)))
}
