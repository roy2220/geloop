package geloop

import "unsafe"

// AttachFileRequest represents the request about attaching a file.
// The request should not be modified since be passed
// as an argument to *Loop.AttachFile call until be released.
type AttachFileRequest struct {
	// The descriptor of the file to attach.
	FD int

	// The optional function called when the file is attached.
	//
	// @param request
	//     The request bound to.
	//
	// @param err
	//     ErrClosed - when the loop is closed;
	//
	// @param ok
	//     The boolean value indicating whether the
	//     file wasn't attached.
	Callback func(request *AttachFileRequest, err error, ok bool)

	// The optional function called when the request is released.
	//
	// @param request
	//     The request bound to.
	Cleanup func(request *AttachFileRequest)

	r request
}

// AttachFile requests to attach the given file to the loop.
func (l *Loop) AttachFile(request1 *AttachFileRequest) {
	if request1.Callback == nil {
		request1.Callback = func(*AttachFileRequest, error, bool) {}
	}

	request1.r.OnTask = func(r *request) bool {
		r1 := getAttachFileRequest(r)
		r1.Callback(r1, nil, r1.r.Loop().attachFile(r1.FD))
		return true
	}

	request1.r.OnError = func(r *request, err error) {
		r1 := getAttachFileRequest(r)
		r1.Callback(r1, err, false)
	}

	request1.r.OnCleanup = func(r *request) {
		r1 := getAttachFileRequest(r)

		if f := r1.Cleanup; f != nil {
			f(r1)
		}
	}

	l.preAddRequest(&request1.r)
}

func getAttachFileRequest(r *request) *AttachFileRequest {
	return (*AttachFileRequest)(unsafe.Pointer(uintptr(unsafe.Pointer(r)) - unsafe.Offsetof(AttachFileRequest{}.r)))
}
