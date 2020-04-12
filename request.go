package geloop

import (
	"sync/atomic"
	"time"

	"github.com/roy2220/geloop/internal/poller"
	"github.com/roy2220/geloop/internal/timer"
	"github.com/roy2220/geloop/internal/worker"
)

type request struct {
	OnTask    func(r *request) (isCompleted bool)
	OnWatch   func(r *request) (isCompleted bool)
	OnAlarm   func(r *request) (isCompleted bool)
	OnError   func(r *request, err error)
	OnCleanup func(r *request)

	id      uint64
	l       *Loop
	isAdded bool
	task    worker.Task
	watch   poller.Watch
	alarm   timer.Alarm
}

func (r *request) Process(l *Loop) uint64 {
	id := atomic.AddUint64(&lastRequestID, 1)

	if !atomic.CompareAndSwapUint64(&r.id, 0, id) {
		panic(errRequestInProcess)
	}

	r.l = l

	if err := l.addTask(&r.task); err != nil {
		r.handleError(err)
		return 0
	}

	return id
}

func (r *request) Cancel() {
	r.handleError(ErrRequestCanceled)
}

func (r *request) AddReadableWatch(fd int) error {
	return r.l.addWatch(&r.watch, fd, poller.EventReadable)
}

func (r *request) AddWritableWatch(fd int) error {
	return r.l.addWatch(&r.watch, fd, poller.EventWritable)
}

func (r *request) AddAlarm(dueTime time.Time) {
	r.l.addAlarm(&r.alarm, dueTime)
}

func (r *request) Loop() *Loop {
	return r.l
}

func (r *request) add() {
	r.l.addRequest(r.id, r)
	r.isAdded = true
}

func (r *request) remove() {
	l := r.l
	l.removeRequest(r.id)
	r.isAdded = false

	if !r.watch.IsReset() {
		l.removeWatch(&r.watch)
	}

	if !r.alarm.IsReset() {
		l.removeAlarm(&r.alarm)
	}
}

func (r *request) release() {
	atomic.StoreUint64(&r.id, 0)
	r.OnCleanup(r)
}

func (r *request) handleTask() {
	if r.OnTask(r) {
		r.remove()
		r.release()
	}
}

func (r *request) handleWatch() {
	if r.OnWatch(r) {
		r.remove()
		r.release()
	}
}

func (r *request) handleAlarm() {
	if r.OnAlarm(r) {
		r.remove()
		r.release()
	}
}

func (r *request) handleError(err error) {
	if r.isAdded {
		r.remove()
	}

	r.OnError(r, err)
	r.release()
}

var lastRequestID = uint64(0)
