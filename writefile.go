package geloop

import (
	"syscall"
	"time"
	"unsafe"

	"github.com/roy2220/geloop/byteslicepool"
)

// WriteFileRequest represents the request about writing a file.
// The request should not be modified since be passed
// as an argument to *Loop.WriteFile call until be released.
type WriteFileRequest struct {
	// The watcher id by adopting the descriptor of the file.
	WatcherID int64

	// The deadline of the request.
	Deadline time.Time

	// The function called before sending data.
	//
	// @param request
	//     The request bound to.
	//
	// @param err
	//     ErrClosed - when the loop has been closed;
	//     ErrRequestCanceled - when the request has been
	//                          canceled;
	//
	// @param buffer
	//     The pointer to shared buffer to put data in.
	//     The shared buffer can be reassigned while meeting
	//     the need to grow.
	//
	// @return dataSize
	//     The size of data put in the buffer.
	PreCallback func(request *WriteFileRequest, err error, buffer *[]byte) (dataSize int)

	// The function called after sending data.
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
	//     the other errors the syscall.Write() returned.
	//
	// @param numberOfBytesSent
	//     The size of data sent.
	PostCallback func(request *WriteFileRequest, err error, numberOfBytesSent int)

	// The optional function called when the request is released.
	//
	// @param request
	//     The request bound to.
	Cleanup func(request *WriteFileRequest)

	numberOfBytesSent int
	unsentData        []byte
	bufferToReuse     []byte

	r request
}

// WriteFile requests to write the given file in the loop.
// It returns the request id for cancellation.
func (l *Loop) WriteFile(request1 *WriteFileRequest) int64 {
	request1.r.OnTask = func(r *request) bool {
		r1 := getWriteFileRequest(r)

		if r1.process1() {
			return true
		}

		if !r1.Deadline.IsZero() {
			r1.r.AddAlarm(r1.Deadline)
		}

		return false
	}

	request1.r.OnWatch = func(r *request) bool {
		r1 := getWriteFileRequest(r)
		return r1.process2()
	}

	request1.r.OnAlarm = func(r *request) bool {
		r1 := getWriteFileRequest(r)
		r1.PostCallback(r1, ErrDeadlineReached, r1.numberOfBytesSent)
		return true
	}

	request1.r.OnError = func(r *request, err error) {
		r1 := getWriteFileRequest(r)

		if r1.unsentData == nil {
			r1.PreCallback(r1, err, nil)
		} else {
			r1.PostCallback(r1, err, r1.numberOfBytesSent)
		}
	}

	request1.r.OnCleanup = func(r *request) {
		r1 := getWriteFileRequest(r)
		r1.numberOfBytesSent = 0
		r1.unsentData = nil
		r1.bufferToReuse = nil

		if f := r1.Cleanup; f != nil {
			f(r1)
		}
	}

	return l.submitRequest(&request1.r)
}

func (r *WriteFileRequest) process1() bool {
	loop := r.r.Loop()
	sharedContext := &loop.writeFileSharedContext
	dataSize := r.PreCallback(r, nil, &sharedContext.Buffer)
	data := sharedContext.Buffer[:dataSize]
	fd, err := loop.getFd(r.WatcherID)

	if err != nil {
		r.PostCallback(r, err, 0)
		return true
	}

	for {
		n, err := syscall.Write(fd, data)

		if err != nil {
			switch err {
			case syscall.EAGAIN:
				n = 0
			case syscall.EINTR:
				continue
			default:
				r.PostCallback(r, err, 0)
				return true
			}
		}

		r.numberOfBytesSent += n

		if r.numberOfBytesSent == dataSize {
			r.PostCallback(r, nil, r.numberOfBytesSent)
			return true
		}

		r.unsentData = append(byteslicepool.Get(), data[r.numberOfBytesSent:]...)
		r.bufferToReuse = r.unsentData
		r.r.AddWritableWatch(r.WatcherID)
		return false
	}

}

func (r *WriteFileRequest) process2() bool {
	fd, err := r.r.Loop().getFd(r.WatcherID)

	if err != nil {
		r.PostCallback(r, err, r.numberOfBytesSent)
		return true
	}

	for {
		n, err := syscall.Write(fd, r.unsentData)

		if err != nil {
			switch err {
			case syscall.EAGAIN:
				n = 0
			case syscall.EINTR:
				continue
			default:
				r.PostCallback(r, err, r.numberOfBytesSent)
				return true
			}
		}

		r.numberOfBytesSent += n
		r.unsentData = r.unsentData[n:]

		if len(r.unsentData) == 0 {
			byteslicepool.Put(r.bufferToReuse)
			r.PostCallback(r, nil, r.numberOfBytesSent)
			return true
		}

		r.r.AddWritableWatch(r.WatcherID)
		return false
	}
}

const initialWriteBufferSize = 1024

type writeFileSharedContext struct {
	Buffer []byte
}

func (wfsc *writeFileSharedContext) Init() {
	wfsc.Buffer = make([]byte, initialWriteBufferSize)
}

func getWriteFileRequest(r *request) *WriteFileRequest {
	return (*WriteFileRequest)(unsafe.Pointer(uintptr(unsafe.Pointer(r)) - unsafe.Offsetof(WriteFileRequest{}.r)))
}
