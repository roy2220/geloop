package geloop

import (
	"syscall"
	"time"
	"unsafe"

	"github.com/roy2220/geloop/internal/poller"
)

// ReadFileRequest represents the request about reading a file.
// The request should not be modified since be passed
// as an argument to *Loop.ReadFile call until be released.
type ReadFileRequest struct {
	// The descriptor of the file to read.
	FD int

	// The deadline of the request.
	Deadline time.Time

	// The function called when there is data received.
	//
	// @param request
	//     The request bound to.
	//
	// @param err
	//     ErrClosed - when the loop is closed;
	//     ErrFileNotAttached - when the file is not attached;
	//     ErrFileDetached - when the file is detached;
	//     ErrDeadlineReached - when the deadline is reached;
	//     ErrNoMoreData - when there is no more data can be
	//                     read from the file, the current
	//                     data must be nil.
	//     ErrRequestCanceled - when the request is canceled;
	//     the other errors the syscall.Read() returned.
	//
	// @param data
	//     The data received from the file in the shared buffer.
	//
	// @param preBuffer
	//     The reserved shared buffer previous (adjacent) to the the data,
	//     with a size not smaller than any data ever received.
	//
	// @return needMoreData
	//     The boolean value indicates whether the request should be continued.
	Callback func(request *ReadFileRequest, err error, data []byte, preBuffer []byte) (needMoreData bool)

	// The optional function called when the request is released.
	//
	// @param request
	//     The request bound to.
	Cleanup func(request *ReadFileRequest)

	r request
}

// ReadFile requests to read the given file in the loop.
// It returns the request id for cancellation.
func (l *Loop) ReadFile(request1 *ReadFileRequest) uint64 {
	request1.r.OnTask = func(r *request) bool {
		r1 := getReadFileRequest(r)

		if isCompleted := r1.process(); isCompleted {
			return true
		}

		if !r1.Deadline.IsZero() {
			r1.r.Loop().addAlarm(&r1.r, r1.Deadline)
		}

		return false
	}

	request1.r.OnWatch = func(r *request) bool {
		r1 := getReadFileRequest(r)
		return r1.process()
	}

	request1.r.OnAlarm = func(r *request) bool {
		r1 := getReadFileRequest(r)
		r1.Callback(r1, ErrDeadlineReached, nil, nil)
		return true
	}

	request1.r.OnError = func(r *request, err error) {
		r1 := getReadFileRequest(r)
		r1.Callback(r1, err, nil, nil)
	}

	request1.r.OnCleanup = func(r *request) {
		r1 := getReadFileRequest(r)

		if f := r1.Cleanup; f != nil {
			f(r1)
		}
	}

	return l.preAddRequest(&request1.r)
}

func getReadFileRequest(r *request) *ReadFileRequest {
	return (*ReadFileRequest)(unsafe.Pointer(uintptr(unsafe.Pointer(r)) - unsafe.Offsetof(ReadFileRequest{}.r)))
}

func (r *ReadFileRequest) process() bool {
	l := r.r.Loop()
	preBuffer := l.readBuffer[:len(l.readBuffer)/2]
	buffer := l.readBuffer[len(preBuffer):]
	i := 0

	for {
		n, err := syscall.Read(r.FD, buffer[i:])

		if err != nil || n == 0 {
			switch err {
			case syscall.EINTR:
				continue
			case syscall.EAGAIN:
				var needMoreData bool

				if i == 0 {
					needMoreData = true
				} else {
					needMoreData = r.Callback(r, nil, buffer[:i], preBuffer)
				}

				if needMoreData {
					if err := l.addWatch(&r.r, r.FD, poller.EventReadable); err != nil {
						r.Callback(r, err, nil, nil)
						return true
					}

					return false
				}

				return true
			default:
				if err == nil {
					err = ErrNoMoreData
				}

				if i == 0 {
					r.Callback(r, err, nil, nil)
				} else {
					needMoreData := r.Callback(r, nil, buffer[:i], preBuffer)

					if needMoreData {
						r.Callback(r, err, nil, nil)
					}
				}

				return true
			}
		}

		i += n

		if i == len(buffer) {
			l.readBuffer = make([]byte, 2*len(l.readBuffer))
			preBuffer = l.readBuffer[:len(l.readBuffer)/2]
			temp := l.readBuffer[len(preBuffer):]
			copy(temp, buffer)
			buffer = temp
		}
	}
}
