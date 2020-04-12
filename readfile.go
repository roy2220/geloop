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
	// The descriptor of the file to read.
	Fd int

	// The deadline of the request.
	Deadline time.Time

	// The function called when there is data received.
	//
	// @param request
	//     The request bound to.
	//
	// @param err
	//     ErrClosed - when the loop has been closed;
	//     ErrInvalidFd - when the fd hasn't yet been adopted
	//                    or has already been closed;
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
func (l *Loop) ReadFile(request1 *ReadFileRequest) uint64 {
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

	return request1.r.Process(l)
}

func getReadFileRequest(r *request) *ReadFileRequest {
	return (*ReadFileRequest)(unsafe.Pointer(uintptr(unsafe.Pointer(r)) - unsafe.Offsetof(ReadFileRequest{}.r)))
}

func (r *ReadFileRequest) process() bool {
	l := r.r.Loop()
	reservedBuffer := l.readBuffer[:l.reservedReadBufferSize]
	buffer := l.readBuffer[l.reservedReadBufferSize:]
	i := 0

	for {
		n, err := syscall.Read(r.Fd, buffer[i:])

		if err != nil || n == 0 {
			switch err {
			case syscall.EINTR:
				continue
			case syscall.EAGAIN:
				var needMoreData bool

				if i == 0 {
					needMoreData = true
				} else {
					needMoreData = r.Callback(r, nil, buffer[:i], reservedBuffer)
				}

				if needMoreData {
					if err := r.r.AddReadableWatch(r.Fd); err != nil {
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
					needMoreData := r.Callback(r, nil, buffer[:i], reservedBuffer)

					if needMoreData {
						r.Callback(r, err, nil, nil)
					}
				}

				return true
			}
		}

		i += n

		if i == len(buffer) {
			l.readBuffer = make([]byte, l.reservedReadBufferSize+2*len(buffer))
			reservedBuffer = l.readBuffer[:l.reservedReadBufferSize]
			temp := l.readBuffer[l.reservedReadBufferSize:]
			copy(temp, buffer)
			buffer = temp
		}
	}
}
