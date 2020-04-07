package geloop

import (
	"sync"
	"unsafe"
)

// CancelRequest requests to cancel a request with the given id.
func (l *Loop) CancelRequest(requestID uint64) {
	if requestID == 0 {
		return
	}

	request1 := allocateCancelRequestRequest()
	request1.IDOfRequestToCancel = requestID

	request1.R.OnTask = func(r *request) bool {
		r1 := getFreeCancelRequestRequest(r)

		if requestToCancel, ok := r1.R.Loop().getRequest(r1.IDOfRequestToCancel); ok {
			requestToCancel.OnError(requestToCancel, ErrRequestCanceled)
			requestToCancel.remove()
			requestToCancel.release()
		}

		return true
	}

	request1.R.OnError = func(*request, error) {}

	request1.R.OnCleanup = func(r *request) {
		r1 := (*cancelRequestRequest)((unsafe.Pointer)(r))
		freeCancelRequestRequest(r1)
	}

	l.preAddRequest(&request1.R)
}

type cancelRequestRequest struct {
	R                   request
	IDOfRequestToCancel uint64
}

var cancelRequestRequestPool = sync.Pool{New: func() interface{} { return new(cancelRequestRequest) }}

func allocateCancelRequestRequest() *cancelRequestRequest {
	return cancelRequestRequestPool.Get().(*cancelRequestRequest)
}

func freeCancelRequestRequest(crr *cancelRequestRequest) {
	*crr = cancelRequestRequest{}
	cancelRequestRequestPool.Put(crr)
}

func getFreeCancelRequestRequest(r *request) *cancelRequestRequest {
	return (*cancelRequestRequest)(unsafe.Pointer(uintptr(unsafe.Pointer(r)) - unsafe.Offsetof(cancelRequestRequest{}.R)))
}
