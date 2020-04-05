// Package geloop implements an event loop.
package geloop

import (
	"errors"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/roy2220/geloop/internal/poller"
	"github.com/roy2220/geloop/internal/timer"
	"github.com/roy2220/geloop/internal/worker"
)

// Loop represents an event loop.
type Loop struct {
	poller        poller.Poller
	timer         timer.Timer
	worker        worker.Worker
	workerWatch   *poller.Watch
	lastRequestID uint64
	requests      map[uint64]*request
	readBuffer    []byte
	writeBuffer   []byte
	isStopped     bool
}

// Init initializes the loop and then returns the loop.
func (l *Loop) Init() *Loop {
	l.poller.Init()
	l.timer.Init()
	l.worker.Init(&l.poller)
	l.workerWatch = &(&request{OnWatch: func(*request) bool { return false }}).watch
	l.isStopped = true
	l.requests = make(map[uint64]*request)
	l.readBuffer = make([]byte, initialReadBufferSize)
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
		request := (*request)(unsafe.Pointer(uintptr(unsafe.Pointer(task)) - unsafe.Offsetof(request{}.task)))
		request.OnError(request, ErrClosed)
		request.release()
	}); err != nil {
		return err
	}

	for _, request := range l.requests {
		request.remove()
		request.release()
	}

	l.timer.Close(func(*timer.Alarm) {})
	return l.poller.Close(func(*poller.Watch) {})
}

// Run runs the loop.
func (l *Loop) Run() {
	l.isStopped = false

	for !l.isStopped {
		l.tick()
	}
}

// Stop requests to stop the loop.
func (l *Loop) Stop() {
	l.preAddRequest(&request{
		OnTask: func(*request) bool {
			l.isStopped = true
			return true
		},

		OnError: func(*request, error) {},
	})
}

func (l *Loop) tick() {
	deadline, _ := l.timer.GetMinDueTime()

	if l.workerWatch.IsReset() {
		if err := l.worker.AddWatch(l.workerWatch); err != nil {
			panic(err)
		}
	}

	// println("--- poller begin ---")
	if err := l.poller.CheckWatches(deadline, func(watch *poller.Watch) {
		request := (*request)(unsafe.Pointer(uintptr(unsafe.Pointer(watch)) - unsafe.Offsetof(request{}.watch)))

		if request.OnWatch(request) {
			request.remove()
			request.release()
		}
	}); err != nil {
		panic(err)
	}
	// println("--- poller end ---")

	// println("--- timer begin ---")
	l.timer.CheckAlarms(func(alarm *timer.Alarm) {
		request := (*request)(unsafe.Pointer(uintptr(unsafe.Pointer(alarm)) - unsafe.Offsetof(request{}.alarm)))

		if request.OnAlarm(request) {
			request.remove()
			request.release()
		}
	})
	// println("--- timer end ---")

	// println("--- worker begin ---")
	if err := l.worker.CheckTasks(func(task *worker.Task) {
		request := (*request)(unsafe.Pointer(uintptr(unsafe.Pointer(task)) - unsafe.Offsetof(request{}.task)))
		request.postAdd()

		if request.OnTask(request) {
			request.remove()
			request.release()
		}
	}); err != nil {
		panic(err)
	}
	// println("--- worker end ---")

}

func (l *Loop) preAddRequest(request *request) uint64 {
	request.l = l
	request.id = atomic.AddUint64(&l.lastRequestID, 1)

	if err := l.addTask(request); err != nil {
		go func() {
			request.OnError(request, err)
			request.release()
		}()

		return 0
	}

	return request.id
}

func (l *Loop) getRequest(requestID uint64) (*request, bool) {
	request, ok := l.requests[requestID]
	return request, ok
}

func (l *Loop) addTask(request *request) error {
	err := l.worker.AddTask(&request.task)

	if err != nil {
		if err != worker.ErrClosed {
			panic(err)
		}

		err = ErrClosed
	}

	return err
}

func (l *Loop) addWatch(request *request, fd int, eventType poller.EventType) error {
	err := l.poller.AddWatch(&request.watch, fd, eventType)

	if err != nil {
		if err != poller.ErrFileNotAttached {
			panic(err)
		}

		err = ErrFileNotAttached
	}

	return err
}

func (l *Loop) removeWatch(request *request) bool {
	return l.poller.RemoveWatch(&request.watch)
}

func (l *Loop) addAlarm(request *request, dueTime time.Time) {
	l.timer.AddAlarm(&request.alarm, dueTime)
}

func (l *Loop) removeAlarm(request *request) {
	l.timer.RemoveAlarm(&request.alarm)
}

func (l *Loop) attachFile(fd int) bool {
	if l.poller.HasFile(fd) {
		return false
	}

	l.poller.AttachFile(fd)
	return true
}

func (l *Loop) detachFile(fd int) bool {
	ok, err := l.poller.DetachFile(fd, func(watch *poller.Watch) {
		request := (*request)(unsafe.Pointer(uintptr(unsafe.Pointer(watch)) - unsafe.Offsetof(request{}.watch)))
		request.OnError(request, ErrFileDetached)
		request.remove()
		request.release()
	})

	if err != nil {
		panic(err)
	}

	return ok
}

var (
	// ErrClosed indicates the loop is closed.
	ErrClosed = errors.New("geloop: closed")

	// ErrFileNotAttached indicates the file to read/write is not yet attached.
	ErrFileNotAttached = errors.New("geloop: file not attached")

	// ErrFileDetached indicates the file has been detached during being read/written.
	ErrFileDetached = errors.New("geloop: file detached")

	// ErrDeadlineReached indicates the deadline is reached before the file is read/written.
	ErrDeadlineReached = errors.New("geloop: deadline reached")

	// ErrNoMoreData indicates there no more data can be read from the file.
	ErrNoMoreData = errors.New("geloop: no more data")

	// ErrRequestCanceled indicates the request is canceled.
	ErrRequestCanceled = errors.New("geloop: request canceled")
)

const (
	initialReadBufferSize  = 2048
	initialWriteBufferSize = 1024
)

type request struct {
	OnTask    func(r *request) (isCompleted bool)
	OnWatch   func(r *request) (isCompleted bool)
	OnAlarm   func(r *request) (isCompleted bool)
	OnError   func(r *request, err error)
	OnCleanup func(r *request)

	l     *Loop
	id    uint64
	task  worker.Task
	watch poller.Watch
	alarm timer.Alarm
}

func (r *request) Loop() *Loop {
	return r.l
}

func (r *request) postAdd() {
	r.l.requests[r.id] = r
}

func (r *request) remove() {
	l := r.l

	if !r.watchIsReset() {
		l.removeWatch(r)
	}

	if !r.alarmIsReset() {
		l.removeAlarm(r)
	}

	delete(l.requests, r.id)
}

func (r *request) release() {
	if f := r.OnCleanup; f != nil {
		f(r)
	}

	r.OnTask = nil
	r.OnWatch = nil
	r.OnAlarm = nil
	r.OnError = nil
	r.OnCleanup = nil
	r.l = nil
}

func (r *request) watchIsReset() bool {
	return r.watch.IsReset()
}

func (r *request) alarmIsReset() bool {
	return r.alarm.IsReset()
}
