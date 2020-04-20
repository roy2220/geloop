// Package geloop implements an event loop.
package geloop

import (
	"runtime"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/roy2220/intrusive"

	"github.com/roy2220/geloop/internal/poller"
	"github.com/roy2220/geloop/internal/timer"
	"github.com/roy2220/geloop/internal/worker"
)

// Loop represents an event loop.
type Loop struct {
	idGenerator

	poller                 poller.Poller
	timer                  timer.Timer
	worker                 worker.Worker
	workerWatch            *poller.Watch
	isBroken               bool
	requestRBTree          intrusive.RBTree
	readFileSharedContext  readFileSharedContext
	writeFileSharedContext writeFileSharedContext
}

// Init initializes the loop and then returns the loop.
func (l *Loop) Init(reservedReadBufferSize int) *Loop {
	l.idGenerator.group(1, 0)
	l.poller.Init()
	l.timer.Init()
	l.worker.Init(&l.poller)
	l.workerWatch = &(&request{OnWatch: func(*request) bool { return false }}).Watch
	l.requestRBTree.Init(orderRequestRBTreeNode, compareRequestRBTreeNode)
	l.readFileSharedContext.Init(reservedReadBufferSize)
	l.writeFileSharedContext.Init()
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

// Close closes the loop. All file descriptors adopted in the
// loop will automatically be closed at once.
func (l *Loop) Close() error {
	if err := l.worker.Close(func(task *worker.Task) {
		request := getRequestByTask(task)
		request.HandleError(ErrClosed)
	}); err != nil {
		return err
	}

	if err := l.poller.Close(func(watch *poller.Watch) {
		request := getRequestByWatch(watch)
		request.HandleError(ErrClosed)
	}); err != nil {
		return err
	}

	l.timer.Close(func(alarm *timer.Alarm) {
		request := getRequestByAlarm(alarm)
		request.HandleError(ErrClosed)
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
	l.submitRequest(&request{
		OnTask: func(*request) bool {
			l.isBroken = true
			return true
		},

		OnError:   func(*request, error) {},
		OnCleanup: func(*request) {},
	})
}

func (l *Loop) iterate() {
	deadline, _ := l.timer.GetMinDueTime()

	if l.workerWatch.IsReset() {
		l.worker.AddWatch(l.workerWatch)
	}

	// println("--- poller begin ---")
	if err := l.poller.ProcessWatches(deadline, func(watch *poller.Watch) {
		request := getRequestByWatch(watch)
		request.HandleWatch()
	}); err != nil {
		panic(err)
	}
	// println("--- poller end ---")

	// println("--- timer begin ---")
	l.timer.ProcessAlarms(func(alarm *timer.Alarm) {
		request := getRequestByAlarm(alarm)
		request.HandleAlarm()
	})
	// println("--- timer end ---")

	// println("--- worker begin ---")
	if err := l.worker.ProcessTasks(func(task *worker.Task) {
		request := getRequestByTask(task)
		request.Add(l)
		request.HandleTask()
	}); err != nil {
		panic(err)
	}
	// println("--- worker end ---")

}

func (l *Loop) submitRequest(request *request) int64 {
	requestID := l.generateRequestID()
	request.Init(requestID)

	if err := l.addTask(request); err != nil {
		request.HandleError(err)
		return 0
	}

	return requestID
}

func (l *Loop) addTask(request *request) error {
	err := l.worker.AddTask(&request.Task)

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

func (l *Loop) addRequest(request *request) {
	l.requestRBTree.InsertNode(&request.RBTreeNode)
}

func (l *Loop) removeRequest(request *request) {
	l.requestRBTree.RemoveNode(&request.RBTreeNode)
}

func (l *Loop) getRequest(requestID int64) (*request, bool) {
	rbTreeNode, ok := l.requestRBTree.FindNode(requestID)

	if !ok {
		return nil, false
	}

	request := getRequest(rbTreeNode)
	return request, true
}

func (l *Loop) adoptFd(fd int) int64 {
	watcherID := l.generateWatcherID()
	l.poller.AdoptFd(fd, watcherID)
	return watcherID
}

func (l *Loop) closeFd(watcherID int64) error {
	err := l.poller.CloseFd(watcherID, func(watch *poller.Watch) {
		r := getRequestByWatch(watch)
		r.HandleError(ErrFdClosed)
	})

	if err != nil {
		switch err {
		case poller.ErrInvalidWatcherID:
			err = ErrInvalidWatcherID
		}
	}

	return err
}

func (l *Loop) getFd(watcherID int64) (int, error) {
	fd, err := l.poller.GetFd(watcherID)

	if err != nil {
		switch err {
		case poller.ErrInvalidWatcherID:
			err = ErrInvalidWatcherID
		}
	}

	return fd, err
}

func (l *Loop) addWatch(request *request, watcherID int64, eventType poller.EventType) {
	l.poller.AddWatch(&request.Watch, watcherID, eventType)
}

func (l *Loop) removeWatch(request *request) {
	l.poller.RemoveWatch(&request.Watch)
}

func (l *Loop) addAlarm(request *request, dueTime time.Time) {
	l.timer.AddAlarm(&request.Alarm, dueTime)
}

func (l *Loop) removeAlarm(request *request) {
	l.timer.RemoveAlarm(&request.Alarm)
}

type idGenerator struct {
	lastRequestIDX2 uint64
	lastWatcherIDX2 uint64
	groupSizeX2     uint64
}

func (idg *idGenerator) group(groupSize int, index int) {
	idg.lastRequestIDX2 = uint64(index << 1)
	idg.lastWatcherIDX2 = uint64(index << 1)
	idg.groupSizeX2 = uint64(groupSize << 1)
}

func (idg *idGenerator) generateRequestID() int64 {
	requestIDX2 := atomic.AddUint64(&idg.lastRequestIDX2, idg.groupSizeX2)

	if requestIDX2 < idg.groupSizeX2 {
		// overflow
		requestIDX2 = atomic.AddUint64(&idg.lastRequestIDX2, idg.groupSizeX2)
	}

	return int64(requestIDX2 >> 1)
}

func (idg *idGenerator) generateWatcherID() int64 {
	idg.lastWatcherIDX2 += idg.groupSizeX2
	watcherIDX2 := idg.lastWatcherIDX2

	if watcherIDX2 < idg.groupSizeX2 {
		// overflow
		idg.lastWatcherIDX2 += idg.groupSizeX2
		watcherIDX2 = idg.lastWatcherIDX2
	}

	return int64(watcherIDX2 >> 1)
}

func getRequest(rbTreeNode *intrusive.RBTreeNode) *request {
	return (*request)(rbTreeNode.GetContainer(unsafe.Offsetof(request{}.RBTreeNode)))
}

func getRequestByTask(task *worker.Task) *request {
	return (*request)(unsafe.Pointer(uintptr(unsafe.Pointer(task)) - unsafe.Offsetof(request{}.Task)))
}

func getRequestByWatch(watch *poller.Watch) *request {
	return (*request)(unsafe.Pointer(uintptr(unsafe.Pointer(watch)) - unsafe.Offsetof(request{}.Watch)))
}

func getRequestByAlarm(alarm *timer.Alarm) *request {
	return (*request)(unsafe.Pointer(uintptr(unsafe.Pointer(alarm)) - unsafe.Offsetof(request{}.Alarm)))
}
