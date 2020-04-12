// Package geloop implements an event loop.
package geloop

import (
	"runtime"
	"time"
	"unsafe"

	"github.com/roy2220/geloop/internal/poller"
	"github.com/roy2220/geloop/internal/timer"
	"github.com/roy2220/geloop/internal/worker"
)

// Loop represents an event loop.
type Loop struct {
	reservedReadBufferSize int
	poller                 poller.Poller
	timer                  timer.Timer
	worker                 worker.Worker
	workerWatch            *poller.Watch
	isBroken               bool
	requests               map[uint64]*request
	readBuffer             []byte
	writeBuffer            []byte
}

// Init initializes the loop and then returns the loop.
func (l *Loop) Init(reservedReadBufferSize int) *Loop {
	l.reservedReadBufferSize = reservedReadBufferSize
	l.poller.Init()
	l.timer.Init()
	l.worker.Init(&l.poller)
	l.workerWatch = &(&request{OnWatch: func(*request) bool { return false }}).watch
	l.requests = make(map[uint64]*request)
	l.readBuffer = make([]byte, reservedReadBufferSize+initialReadBufferSize)
	l.writeBuffer = make([]byte, initialWriteBufferSize)
	return l
}

// Open opens the loop.
func (l *Loop) Open() error {
	if err := l.poller.Open(); err != nil {
		return err
	}

	if err := l.worker.Open(); err != nil {
		l.poller.Close(nil)
		return err
	}

	return nil
}

// Close closes the loop.
func (l *Loop) Close() error {
	if err := l.worker.Close(func(task *worker.Task) {
		request := getRequestByTask(task)
		request.handleError(ErrClosed)
	}); err != nil {
		return err
	}

	if err := l.poller.Close(func(watch *poller.Watch) {
		request := getRequestByWatch(watch)
		request.handleError(ErrClosed)
	}); err != nil {
		return err
	}

	l.timer.Close(func(alarm *timer.Alarm) {
		request := getRequestByAlarm(alarm)
		request.handleError(ErrClosed)
	})

	return nil
}

// Run runs the loop.
func (l *Loop) Run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	l.isBroken = false

	for !l.isBroken {
		l.iterate()
	}
}

// Stop requests to stop the loop.
func (l *Loop) Stop() {
	(&request{
		OnTask: func(*request) bool {
			l.isBroken = true
			return true
		},

		OnError:   func(*request, error) {},
		OnCleanup: func(*request) {},
	}).Process(l)
}

func (l *Loop) iterate() {
	deadline, _ := l.timer.GetMinDueTime()

	if l.workerWatch.IsReset() {
		if err := l.worker.AddWatch(l.workerWatch); err != nil {
			panic(err)
		}
	}

	// println("--- poller begin ---")
	if err := l.poller.ProcessWatches(deadline, func(watch *poller.Watch) {
		request := getRequestByWatch(watch)
		request.handleWatch()
	}); err != nil {
		panic(err)
	}
	// println("--- poller end ---")

	// println("--- timer begin ---")
	l.timer.ProcessAlarms(func(alarm *timer.Alarm) {
		request := getRequestByAlarm(alarm)
		request.handleAlarm()
	})
	// println("--- timer end ---")

	// println("--- worker begin ---")
	if err := l.worker.ProcessTasks(func(task *worker.Task) {
		request := getRequestByTask(task)
		request.add()
		request.handleTask()
	}); err != nil {
		panic(err)
	}
	// println("--- worker end ---")

}

func (l *Loop) addTask(task *worker.Task) error {
	err := l.worker.AddTask(task)

	if err != nil {
		switch err {
		case worker.ErrClosed:
			err = ErrClosed
		default:
			panic(err)
		}
	}

	return err
}

func (l *Loop) adoptFd(fd int) {
	l.poller.AdoptFd(fd)
}

func (l *Loop) closeFd(fd int) error {
	err := l.poller.CloseFd(fd, func(watch *poller.Watch) {
		r := getRequestByWatch(watch)
		r.handleError(ErrFdClosed)
	})

	if err != nil {
		switch err {
		case poller.ErrInvalidFd:
			err = ErrInvalidFd
		}
	}

	return err
}

func (l *Loop) addWatch(watch *poller.Watch, fd int, eventType poller.EventType) error {
	err := l.poller.AddWatch(watch, fd, eventType)

	if err != nil {
		switch err {
		case poller.ErrInvalidFd:
			err = ErrInvalidFd
		default:
			panic(err)
		}
	}

	return err
}

func (l *Loop) removeWatch(watch *poller.Watch) {
	l.poller.RemoveWatch(watch)
}

func (l *Loop) addAlarm(alarm *timer.Alarm, dueTime time.Time) {
	l.timer.AddAlarm(alarm, dueTime)
}

func (l *Loop) removeAlarm(alarm *timer.Alarm) {
	l.timer.RemoveAlarm(alarm)
}

func (l *Loop) addRequest(requestID uint64, request *request) {
	l.requests[requestID] = request
}

func (l *Loop) removeRequest(requestID uint64) {
	delete(l.requests, requestID)
}

func (l *Loop) getRequest(requestID uint64) (*request, bool) {
	request, ok := l.requests[requestID]
	return request, ok
}

const (
	initialReadBufferSize  = 1024
	initialWriteBufferSize = 1024
)

func getRequestByTask(task *worker.Task) *request {
	return (*request)(unsafe.Pointer(uintptr(unsafe.Pointer(task)) - unsafe.Offsetof(request{}.task)))
}

func getRequestByWatch(watch *poller.Watch) *request {
	return (*request)(unsafe.Pointer(uintptr(unsafe.Pointer(watch)) - unsafe.Offsetof(request{}.watch)))
}

func getRequestByAlarm(alarm *timer.Alarm) *request {
	return (*request)(unsafe.Pointer(uintptr(unsafe.Pointer(alarm)) - unsafe.Offsetof(request{}.alarm)))
}
