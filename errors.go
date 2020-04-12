package geloop

import "errors"

var (
	// ErrClosed indicates the loop has been closed.
	ErrClosed = errors.New("geloop: closed")

	// ErrInvalidFd indicates the file descriptor is invalid.
	ErrInvalidFd = errors.New("geloop: invalid fd")

	// ErrFdClosed indicates the file descriptor has been closed.
	ErrFdClosed = errors.New("geloop: fd closed")

	// ErrRequestCanceled indicates the request has been canceled.
	ErrRequestCanceled = errors.New("geloop: request canceled")

	// ErrDeadlineReached indicates the deadline has been reached.
	ErrDeadlineReached = errors.New("geloop: deadline reached")

	// ErrNoMoreData indicates there is no more data.
	ErrNoMoreData = errors.New("geloop: no more data")
)

var errRequestInProcess = errors.New("geloop: request in process")
