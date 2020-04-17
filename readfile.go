package geloop

import (
	"syscall"
	"time"
	"unsafe"
)

// ReadFileRequest represents the request about reading a file.
// The request should not be modified since be passed
// as an argument to *Loop.ReadFile call until be released.
type ReadFileRequest struct {
	// The watcher id by adopting the descriptor of the file.
	WatcherID int64

	// The deadline of the request.
	Deadline time.Time

	// The function called when there is data received.
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
	//     ErrNoMoreData - when there is no more data can be
	//                     read from the file;
	//     the other errors the syscall.Read() returned.
	//
	// @param data
	//     The data received from the file in the shared buffer.
	//
	// @param reservedBuffer
	//     The reserved shared buffer preceding to the the data, with a
	//     size specified on the loop initialization, for prepending
	//     custom data to the data for any purposes.
	//
	// @return needMoreData
	//     The boolean value indicates whether the request should be continued.
	Callback func(request *ReadFileRequest, err error, data []byte, reservedBuffer []byte) (needMoreData bool)

	// The optional function called when the request is released.
	//
	// @param request
	//     The request bound to.
	Cleanup func(request *ReadFileRequest)

	r request
}

// ReadFile requests to read the given file in the loop.
// It returns the request id for cancellation.
func (l *Loop) ReadFile(request1 *ReadFileRequest) int64 {
	request1.r.OnTask = func(r *request) bool {
		r1 := getReadFileRequest(r)

		if r1.process() {
			return true
		}

		if !r1.Deadline.IsZero() {
			r1.r.AddAlarm(r1.Deadline)
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

	return request1.r.Submit(l)
}

func (r *ReadFileRequest) process() bool {
	loop := r.r.Loop()
	fd, err := loop.getFd(r.WatcherID)

	if err != nil {
		r.Callback(r, err, nil, nil)
		return true
	}

	reservedBuffer := loop.readBuffer[:loop.reservedReadBufferSize]
	buffer := loop.readBuffer[loop.reservedReadBufferSize:]
	i := 0

	for {
		n, err := syscall.Read(fd, buffer[i:])

		if err != nil || n == 0 {
			switch err {
			case syscall.EAGAIN:
				n = 0
			case syscall.EINTR:
				continue
			default:
				if err == nil {
					err = ErrNoMoreData
				}

				if i == 0 || r.Callback(r, nil, buffer[:i], reservedBuffer) {
					r.Callback(r, err, nil, nil)
				}

				return true
			}
		}

		i += n

		if i < len(buffer) {
			if i == 0 || r.Callback(r, nil, buffer[:i], reservedBuffer) {
				r.r.AddReadableWatch(r.WatcherID)
				return false
			}

			return true
		}

		loop.readBuffer = make([]byte, loop.reservedReadBufferSize+2*len(buffer))
		reservedBuffer = loop.readBuffer[:loop.reservedReadBufferSize]
		temp := loop.readBuffer[loop.reservedReadBufferSize:]
		copy(temp, buffer)
		buffer = temp
	}
}

func getReadFileRequest(r *request) *ReadFileRequest {
	return (*ReadFileRequest)(unsafe.Pointer(uintptr(unsafe.Pointer(r)) - unsafe.Offsetof(ReadFileRequest{}.r)))
}
