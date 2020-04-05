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
	poller       *poller.Poller
	fd           int
	taskList     intrusive.List
	taskListLock sync.Mutex
	isClosed     bool
}

// Init ...
func (w *Worker) Init(poller *poller.Poller) *Worker {
	w.poller = poller
	w.fd = -1
	w.taskList.Init()
	return w
}

// Open ...
func (w *Worker) Open() error {
	r1, _, err := syscall.Syscall(syscall.SYS_EVENTFD2, 0, syscall.O_CLOEXEC, 0)

	if err != 0 {
		return err
	}

	w.fd = int(r1)
	w.poller.AttachFile(w.fd)
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

	if _, err := w.poller.DetachFile(w.fd, func(*poller.Watch) {}); err != nil {
		return err
	}

	return syscall.Close(w.fd)
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

	hadNoTask := w.taskList.IsEmpty()
	w.taskList.AppendNode(&task.listNode)

	if hadNoTask {
		x := uint64(1)
		_, err := syscall.Write(w.fd, (*(*[8]byte)(unsafe.Pointer(&x)))[:])
		return err
	}

	return nil
}

// CheckTasks ...
func (w *Worker) CheckTasks(callback func(*Task)) error {
	taskList := new(intrusive.List).Init()

	if err := w.removeTasks(taskList); err != nil {
		return err
	}

	for listHead := taskList.Head(); !listHead.IsNull(taskList); listHead = taskList.Head() {
		listHead.Remove()
		task := (*Task)(listHead.GetContainer(unsafe.Offsetof(Task{}.listNode)))
		*task = Task{}
		callback(task)
	}

	return nil
}

func (w *Worker) removeTasks(taskList *intrusive.List) error {
	w.taskListLock.Lock()
	defer w.taskListLock.Unlock()

	if w.taskList.IsEmpty() {
		return nil
	}

	taskList.AppendNodes(&w.taskList)
	var temp [8]byte
	_, err := syscall.Read(w.fd, temp[:])
	return err
}

// Task ...
type Task struct {
	listNode intrusive.ListNode
}

// ErrClosed ...
var ErrClosed = errors.New("worker: closed")
