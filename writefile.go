package geloop

import (
	"syscall"
	"time"
	"unsafe"

	"github.com/roy2220/geloop/internal/poller"
)

// WriteFileRequest represents the request about writing a file.
// The request should not be modified since be passed
// as an argument to *Loop.WriteFile call until be released.
type WriteFileRequest struct {
	// The descriptor of the file to write.
	FD int

	// The deadline of the request.
	Deadline time.Time

	// The function called when the file is ready to write.
	//
	// @param request
	//     The request bound to.
	//
	// @param err
	//     ErrClosed - when the loop is closed;
	//     ErrFileNotAttached - when the file is not attached;
	//     ErrFileDetached - when the file is detached;
	//     ErrDeadlineReached - when the deadline is reached;
	//     ErrRequestCanceled - when the request is canceled;
	//
	// @param buffer
	//     The pointer to shared buffer to put data in.
	//     The shared buffer can be reassigned while meeting
	//     the need to grow.
	//
	// @return dataSize
	//     The size of data put in the buffer.
	PreCallback func(request *WriteFileRequest, err error, buffer *[]byte) (dataSize int)

	// The function called when data is sent.
	//
	// @param request
	//     The request bound to.
	//
	// @param err
	//     ErrClosed - when the loop is closed;
	//     ErrFileDetached - when the file is detached;
	//     ErrDeadlineReached - when the deadline is reached;
	//     ErrRequestCanceled - when the request is canceled;
	//     the other errors the syscall.Write() returned.
	//
	// @param numberOfBytesWritten
	//     The size of data sent.
	PostCallback func(request *WriteFileRequest, err error, numberOfBytesWritten int)

	// The optional function called when the request is released.
	//
	// @param request
	//     The request bound to.
	Cleanup func(request *WriteFileRequest)

	r                    request
	numberOfBytesWritten int
	unsentData           []byte
}

// WriteFile requests to write the given file in the loop.
// It returns the request id for cancellation.
func (l *Loop) WriteFile(request1 *WriteFileRequest) uint64 {
	request1.r.OnTask = func(r *request) bool {
		r1 := getWriteFileRequest(r)
		l := r1.r.Loop()

		if err := l.addWatch(&r1.r, r1.FD, poller.EventWritable); err != nil {
			r1.PreCallback(r1, err, nil)
			return true
		}

		if !r1.Deadline.IsZero() {
			l.addAlarm(&r1.r, r1.Deadline)
		}

		return false
	}

	request1.r.OnWatch = func(r *request) bool {
		r1 := getWriteFileRequest(r)
		var isCompleted bool

		if r1.numberOfBytesWritten == 0 {
			isCompleted = r1.onWatch1()
		} else {
			isCompleted = r1.onWatch2()
		}

		return isCompleted
	}

	request1.r.OnAlarm = func(r *request) bool {
		r1 := getWriteFileRequest(r)

		if r1.numberOfBytesWritten == 0 {
			r1.PreCallback(r1, ErrDeadlineReached, nil)
		} else {
			r1.PostCallback(r1, ErrDeadlineReached, r1.numberOfBytesWritten)
		}

		return true
	}

	request1.r.OnError = func(r *request, err error) {
		r1 := getWriteFileRequest(r)

		if r1.numberOfBytesWritten == 0 {
			r1.PreCallback(r1, err, nil)
		} else {
			r1.PostCallback(r1, err, r1.numberOfBytesWritten)
		}
	}

	request1.r.OnCleanup = func(r *request) {
		r1 := getWriteFileRequest(r)

		if f := r1.Cleanup; f != nil {
			f(r1)
		}
	}

	return l.preAddRequest(&request1.r)
}

func (r *WriteFileRequest) onWatch1() bool {
	l := r.r.Loop()
	dataSize := r.PreCallback(r, nil, &l.writeBuffer)

	for {
		unsentData := l.writeBuffer[r.numberOfBytesWritten:dataSize]
		n, err := syscall.Write(r.FD, unsentData)

		if err != nil {
			switch err {
			case syscall.EINTR:
				continue
			case syscall.EAGAIN:
				r.unsentData = make([]byte, len(unsentData))
				copy(r.unsentData, unsentData)
				l.addWatch(&r.r, r.FD, poller.EventWritable)
				return false
			default:
				r.PostCallback(r, err, r.numberOfBytesWritten)
				return true
			}
		}

		r.numberOfBytesWritten += n

		if r.numberOfBytesWritten == dataSize {
			r.PostCallback(r, nil, r.numberOfBytesWritten)
			return true
		}
	}
}

func (r *WriteFileRequest) onWatch2() bool {
	for {
		n, err := syscall.Write(r.FD, r.unsentData)

		if err != nil {
			switch err {
			case syscall.EINTR:
				continue
			case syscall.EAGAIN:
				r.r.Loop().addWatch(&r.r, r.FD, poller.EventWritable)
				return false
			default:
				r.PostCallback(r, err, r.numberOfBytesWritten)
				return true
			}
		}

		r.numberOfBytesWritten += n
		r.unsentData = r.unsentData[n:]

		if len(r.unsentData) == 0 {
			r.PostCallback(r, nil, r.numberOfBytesWritten)
			return true
		}
	}
}

func getWriteFileRequest(r *request) *WriteFileRequest {
	return (*WriteFileRequest)(unsafe.Pointer(uintptr(unsafe.Pointer(r)) - unsafe.Offsetof(WriteFileRequest{}.r)))
}
