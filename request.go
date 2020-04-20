package geloop

import (
	"sync/atomic"
	"time"

	"github.com/roy2220/intrusive"

	"github.com/roy2220/geloop/internal/poller"
	"github.com/roy2220/geloop/internal/timer"
	"github.com/roy2220/geloop/internal/worker"
)

type request struct {
	RBTreeNode intrusive.RBTreeNode
	Task       worker.Task
	OnTask     func(r *request) (isCompleted bool)
	Watch      poller.Watch
	OnWatch    func(r *request) (isCompleted bool)
	Alarm      timer.Alarm
	OnAlarm    func(r *request) (isCompleted bool)
	OnError    func(r *request, err error)
	OnCleanup  func(r *request)

	id   int64
	loop *Loop
}

func (r *request) Init(id int64) {
	if !atomic.CompareAndSwapInt64(&r.id, 0, id) {
		panic(errRequestInProcess)
	}
}

func (r *request) Cancel() {
	r.HandleError(ErrRequestCanceled)
}

func (r *request) Add(loop *Loop) {
	loop.addRequest(r)
	r.loop = loop
}

func (r *request) AddReadableWatch(watcherID int64) {
	r.loop.addWatch(r, watcherID, poller.EventReadable)
}

func (r *request) AddWritableWatch(watcherID int64) {
	r.loop.addWatch(r, watcherID, poller.EventWritable)
}

func (r *request) AddAlarm(dueTime time.Time) {
	r.loop.addAlarm(r, dueTime)
}

func (r *request) HandleTask() {
	if r.OnTask(r) {
		r.remove()
		r.release()
	}
}

func (r *request) HandleWatch() {
	if r.OnWatch(r) {
		r.remove()
		r.release()
	}
}

func (r *request) HandleAlarm() {
	if r.OnAlarm(r) {
		r.remove()
		r.release()
	}
}

func (r *request) HandleError(err error) {
	if r.loop != nil {
		r.remove()
	}

	r.OnError(r, err)
	r.release()
}

func (r *request) Loop() *Loop {
	return r.loop
}

func (r *request) remove() {
	loop := r.loop
	r.loop = nil
	loop.removeRequest(r)

	if !r.Watch.IsReset() {
		loop.removeWatch(r)
	}

	if !r.Alarm.IsReset() {
		loop.removeAlarm(r)
	}
}

func (r *request) release() {
	atomic.StoreInt64(&r.id, 0)
	r.OnCleanup(r)
}

func orderRequestRBTreeNode(rbTreeNode1, rbTreeNode2 *intrusive.RBTreeNode) bool {
	request1 := getRequest(rbTreeNode1)
	request2 := getRequest(rbTreeNode2)
	return request1.id < request2.id
}

func compareRequestRBTreeNode(rbTreeNode *intrusive.RBTreeNode, requestID interface{}) int64 {
	request := getRequest(rbTreeNode)
	return int64(request.id - requestID.(int64))
}
