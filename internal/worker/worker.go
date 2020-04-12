package worker

import (
	"errors"
	"sync"
	"syscall"
	"unsafe"

	"github.com/roy2220/geloop/internal/poller"
	"github.com/roy2220/intrusive"
)

// Worker ...
type Worker struct {
	poller                    *poller.Poller
	fd                        int
	taskList                  intrusive.List
	taskListLock              sync.Mutex
	isClosed                  bool
	averageProcessedTaskCount mean
}

// Init ...
func (w *Worker) Init(poller *poller.Poller) *Worker {
	w.poller = poller
	w.fd = -1
	w.taskList.Init()
	w.averageProcessedTaskCount.Init(1000)
	return w
}

// Open ...
func (w *Worker) Open() error {
	r1, _, err := syscall.Syscall(syscall.SYS_EVENTFD2, 0, syscall.O_CLOEXEC, 0)

	if err != 0 {
		return err
	}

	fd := int(r1)
	w.poller.AdoptFd(fd)
	w.fd = fd
	return nil
}

// Close ...
func (w *Worker) Close(callback func(*Task)) error {
	func() {
		w.taskListLock.Lock()
		defer w.taskListLock.Unlock()
		w.isClosed = true
	}()

	for listHead := w.taskList.Head(); !listHead.IsNull(&w.taskList); listHead = w.taskList.Head() {
		listHead.Remove()
		task := (*Task)(listHead.GetContainer(unsafe.Offsetof(Task{}.listNode)))
		*task = Task{}
		callback(task)
	}

	w.taskList = intrusive.List{}
	return w.poller.CloseFd(w.fd, func(*poller.Watch) {})
}

// AddWatch ...
func (w *Worker) AddWatch(watch *poller.Watch) error {
	return w.poller.AddWatch(watch, w.fd, poller.EventReadable)
}

// AddTask ...
func (w *Worker) AddTask(task *Task) error {
	w.taskListLock.Lock()
	defer w.taskListLock.Unlock()

	if w.isClosed {
		return ErrClosed
	}

	if w.taskList.IsEmpty() {
		x := uint64(1)

		if _, err := syscall.Write(w.fd, (*(*[8]byte)(unsafe.Pointer(&x)))[:]); err != nil {
			return err
		}
	}

	w.taskList.AppendNode(&task.listNode)
	return nil
}

// ProcessTasks ...
func (w *Worker) ProcessTasks(callback func(*Task)) error {
	taskList := new(intrusive.List).Init()
	averageProcessedTaskCount := w.averageProcessedTaskCount.Calculate()
	processedTaskCount := int64(0)

	for {
		ok, err := w.removeTasks(taskList)

		if !ok {
			w.averageProcessedTaskCount.UpdateSample(processedTaskCount)
			return err
		}

		for listHead := taskList.Head(); !listHead.IsNull(taskList); listHead = taskList.Head() {
			listHead.Remove()
			task := (*Task)(listHead.GetContainer(unsafe.Offsetof(Task{}.listNode)))
			*task = Task{}
			callback(task)
			processedTaskCount++
		}

		if processedTaskCount >= averageProcessedTaskCount {
			w.averageProcessedTaskCount.UpdateSample(processedTaskCount)
			return nil
		}
	}
}

func (w *Worker) removeTasks(taskList *intrusive.List) (bool, error) {
	w.taskListLock.Lock()
	defer w.taskListLock.Unlock()

	if w.taskList.IsEmpty() {
		return false, nil
	}

	var temp [8]byte

	if _, err := syscall.Read(w.fd, temp[:]); err != nil {
		return false, err
	}

	taskList.AppendNodes(&w.taskList)
	return true, nil
}

// Task ...
type Task struct {
	listNode intrusive.ListNode
}

// ErrClosed ...
var ErrClosed = errors.New("worker: closed")
