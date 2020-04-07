package geloop

import (
	"syscall"
	"time"
	"unsafe"

	"github.com/roy2220/geloop/byteslicepool"
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

	// The function called before sending data.
	//
	// @param request
	//     The request bound to.
	//
	// @param err
	//     ErrClosed - when the loop is closed;
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

	// The function called after sending data.
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

	r                 request
	numberOfBytesSent int
	unsentData        []byte
	bufferToReuse     []byte
}

// WriteFile requests to write the given file in the loop.
// It returns the request id for cancellation.
func (l *Loop) WriteFile(request1 *WriteFileRequest) uint64 {
	request1.r.OnTask = func(r *request) bool {
		r1 := getWriteFileRequest(r)

		if isCompleted := r1.process(); isCompleted {
			return true
		}

		if !r1.Deadline.IsZero() {
			r1.r.Loop().addAlarm(&r1.r, r1.Deadline)
		}

		return false
	}

	request1.r.OnWatch = func(r *request) bool {
		r1 := getWriteFileRequest(r)
		return r1.process()
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

		if f := r1.Cleanup; f != nil {
			f(r1)
		}
	}

	return l.preAddRequest(&request1.r)
}

func (r *WriteFileRequest) process() bool {
	l := r.r.Loop()

	if r.unsentData == nil {
		dataSize := r.PreCallback(r, nil, &l.writeBuffer)

		for {
			unsentData := l.writeBuffer[r.numberOfBytesSent:dataSize]
			n, err := syscall.Write(r.FD, unsentData)

			if err != nil {
				switch err {
				case syscall.EINTR:
					continue
				case syscall.EAGAIN:
					r.unsentData = append(byteslicepool.Get(), unsentData...)
					r.bufferToReuse = r.unsentData

					if err := l.addWatch(&r.r, r.FD, poller.EventWritable); err != nil {
						r.PostCallback(r, err, r.numberOfBytesSent)
						return true
					}

					return false
				default:
					r.PostCallback(r, err, r.numberOfBytesSent)
					return true
				}
			}

			r.numberOfBytesSent += n

			if r.numberOfBytesSent == dataSize {
				r.PostCallback(r, nil, r.numberOfBytesSent)
				return true
			}
		}
	}

	for {
		n, err := syscall.Write(r.FD, r.unsentData)

		if err != nil {
			switch err {
			case syscall.EINTR:
				continue
			case syscall.EAGAIN:
				if err := l.addWatch(&r.r, r.FD, poller.EventWritable); err != nil {
					r.PostCallback(r, err, r.numberOfBytesSent)
					return true
				}

				return false
			default:
				r.PostCallback(r, err, r.numberOfBytesSent)
				return true
			}
		}

		r.numberOfBytesSent += n
		r.unsentData = r.unsentData[n:]

		if len(r.unsentData) == 0 {
			r.unsentData = nil
			byteslicepool.Put(r.bufferToReuse)
			r.bufferToReuse = nil
			r.PostCallback(r, nil, r.numberOfBytesSent)
			return true
		}
	}
}

func getWriteFileRequest(r *request) *WriteFileRequest {
	return (*WriteFileRequest)(unsafe.Pointer(uintptr(unsafe.Pointer(r)) - unsafe.Offsetof(WriteFileRequest{}.r)))
}
