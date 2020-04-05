package poller

import (
	"errors"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/roy2220/intrusive"
)

const (
	// EventReadable ...
	EventReadable = EventType(1 + iota)

	// EventWritable ...
	EventWritable

	numberOfEventTypes = iota
)

// Poller ...
type Poller struct {
	fd            int
	fileRBTree    intrusive.RBTree
	dirtyFileList intrusive.List
	eventsBuffer  []syscall.EpollEvent
}

// Init ...
func (p *Poller) Init() *Poller {
	p.fd = -1

	p.fileRBTree.Init(
		func(rbTreeNode1, rbTreeNode2 *intrusive.RBTreeNode) bool {
			file1 := (*file)(rbTreeNode1.GetContainer(unsafe.Offsetof(file{}.RBTreeNode)))
			file2 := (*file)(rbTreeNode2.GetContainer(unsafe.Offsetof(file{}.RBTreeNode)))
			return file1.FD < file2.FD
		},

		func(rbTreeNode1 *intrusive.RBTreeNode, fd interface{}) int64 {
			file := (*file)(rbTreeNode1.GetContainer(unsafe.Offsetof(file{}.RBTreeNode)))
			return int64(file.FD - fd.(int))
		},
	)

	p.dirtyFileList.Init()
	p.eventsBuffer = make([]syscall.EpollEvent, initialEventBufferLength)
	return p
}

// Open ...
func (p *Poller) Open() error {
	fd, err := syscall.EpollCreate1(syscall.O_CLOEXEC)

	if err != nil {
		return err
	}

	p.fd = fd
	return nil
}

// Close ...
func (p *Poller) Close(callback func(*Watch)) error {
	for rbTreeRoot, ok := p.fileRBTree.GetRoot(); ok; rbTreeRoot, ok = p.fileRBTree.GetRoot() {
		p.fileRBTree.RemoveNode(rbTreeRoot)
		file := (*file)(rbTreeRoot.GetContainer(unsafe.Offsetof(file{}.RBTreeNode)))
		var watchLists [numberOfEventTypes]intrusive.List

		for i := range file.WatchLists {
			watchLists[i].Init()
			watchLists[i].AppendNodes(&file.WatchLists[i])
		}

		freeFile(file)
		p.iterateWatches(&watchLists, callback)
	}

	p.fileRBTree = intrusive.RBTree{}
	p.dirtyFileList = intrusive.List{}
	return syscall.Close(p.fd)
}

// AttachFile ...
func (p *Poller) AttachFile(fd int) {
	file := allocateFile()
	file.FD = fd

	for i := range file.WatchLists {
		file.WatchLists[i].Init()
	}

	p.fileRBTree.InsertNode(&file.RBTreeNode)
}

// DetachFile ...
func (p *Poller) DetachFile(fd int, callback func(*Watch)) (bool, error) {
	rbTreeNode, ok := p.fileRBTree.FindNode(fd)

	if !ok {
		return false, nil
	}

	p.fileRBTree.RemoveNode(rbTreeNode)
	file := (*file)(rbTreeNode.GetContainer(unsafe.Offsetof(file{}.RBTreeNode)))

	if !file.DirtyListNode.IsReset() {
		file.DirtyListNode.Remove()
	}

	var watchLists [numberOfEventTypes]intrusive.List

	for i := range file.WatchLists {
		watchLists[i].Init()
		watchLists[i].AppendNodes(&file.WatchLists[i])
	}

	watchedEventTypes := file.WatchedEventTypes
	freeFile(file)
	p.iterateWatches(&watchLists, callback)

	if watchedEventTypes != 0 {
		if err := syscall.EpollCtl(p.fd, syscall.EPOLL_CTL_DEL, fd, nil); err != nil {
			return false, err
		}
	}

	return true, nil
}

// HasFile ...
func (p *Poller) HasFile(fd int) bool {
	_, ok := p.fileRBTree.FindNode(fd)
	return ok
}

// AddWatch ...
func (p *Poller) AddWatch(watch *Watch, fd int, eventType EventType) error {
	rbTreeNode, ok := p.fileRBTree.FindNode(fd)

	if !ok {
		return ErrFileNotAttached
	}

	file := (*file)(rbTreeNode.GetContainer(unsafe.Offsetof(file{}.RBTreeNode)))
	watch.file = file
	watch.eventType = eventType
	watchList := &file.WatchLists[eventType-1]
	hadNoWatch := watchList.IsEmpty()
	watchList.AppendNode(&watch.listNode)

	if hadNoWatch && file.DirtyListNode.IsReset() {
		p.dirtyFileList.AppendNode(&file.DirtyListNode)
	}

	return nil
}

// RemoveWatch ...
func (p *Poller) RemoveWatch(watch *Watch) bool {
	if watch.listNode.IsReset() {
		return false
	}

	watch.listNode.Remove()
	file := watch.file
	eventType := watch.eventType
	*watch = Watch{}

	if file.WatchLists[eventType-1].IsEmpty() && file.DirtyListNode.IsReset() {
		p.dirtyFileList.AppendNode(&file.DirtyListNode)
	}

	return true
}

// CheckWatches ...
func (p *Poller) CheckWatches(deadline time.Time, callback func(*Watch)) error {
	if err := p.flushFiles(); err != nil {
		return err
	}

	var timeoutMs int

	if deadline.IsZero() {
		timeoutMs = -1
	} else {
		timeoutMs = int(time.Until(deadline).Milliseconds())

		if timeoutMs < 0 {
			timeoutMs = 0
		}
	}

	for {
		numberOfEvents, err := syscall.EpollWait(p.fd, p.eventsBuffer, timeoutMs)

		if err != nil {
			if err == syscall.EINTR {
				if timeoutMs >= 1 {
					timeoutMs = int(time.Until(deadline).Milliseconds())

					if timeoutMs < 0 {
						timeoutMs = 0
					}
				}

				continue
			}

			return err
		}

		events := p.eventsBuffer[:numberOfEvents]

		for _, event := range events {
			rbTreeNode, ok := p.fileRBTree.FindNode(int(event.Fd))

			if !ok {
				continue
			}

			file := (*file)(rbTreeNode.GetContainer(unsafe.Offsetof(file{}.RBTreeNode)))
			var watchLists [numberOfEventTypes]intrusive.List

			for i := range file.WatchLists {
				watchLists[i].Init()
			}

			if event.Events&syscall.EPOLLIN != 0 {
				watchLists[EventReadable-1].AppendNodes(&file.WatchLists[EventReadable-1])
			}

			if event.Events&syscall.EPOLLOUT != 0 {
				watchLists[EventWritable-1].AppendNodes(&file.WatchLists[EventWritable-1])
			}

			p.dirtyFileList.AppendNode(&file.DirtyListNode)
			p.iterateWatches(&watchLists, callback)
		}

		if numberOfEvents < len(p.eventsBuffer) {
			break
		}

		p.eventsBuffer = make([]syscall.EpollEvent, 2*len(p.eventsBuffer))
		timeoutMs = 0
	}

	return nil
}

func (p *Poller) flushFiles() error {
	for it := p.dirtyFileList.GetNodes(); !it.IsAtEnd(); it.Advance() {
		file := (*file)(it.Node().GetContainer(unsafe.Offsetof(file{}.DirtyListNode)))
		file.DirtyListNode = intrusive.ListNode{}
		oldWatchedEventTypes := file.WatchedEventTypes
		file.WatchedEventTypes = 0

		for i := range file.WatchLists {
			if !file.WatchLists[i].IsEmpty() {
				file.WatchedEventTypes |= 1 << i
			}
		}

		if file.WatchedEventTypes == oldWatchedEventTypes {
			continue
		}

		var op int
		var event syscall.EpollEvent

		if file.WatchedEventTypes == 0 {
			op = syscall.EPOLL_CTL_DEL
		} else {
			if oldWatchedEventTypes == 0 {
				op = syscall.EPOLL_CTL_ADD
			} else {
				op = syscall.EPOLL_CTL_MOD
			}

			event.Fd = int32(file.FD)
			event.Events = uint32(syscall.EPOLLET & 0xFFFFFFFF)

			if file.WatchedEventTypes&(1<<(EventReadable-1)) != 0 {
				event.Events |= syscall.EPOLLIN
			}

			if file.WatchedEventTypes&(1<<(EventWritable-1)) != 0 {
				event.Events |= syscall.EPOLLOUT
			}
		}

		if err := syscall.EpollCtl(p.fd, op, file.FD, &event); err != nil {
			return err
		}
	}

	p.dirtyFileList.Init()
	return nil
}

func (p *Poller) iterateWatches(watchLists *[numberOfEventTypes]intrusive.List, callback func(*Watch)) {
	for i := range watchLists {
		watchList := &watchLists[i]

		for listHead := watchList.Head(); !listHead.IsNull(watchList); listHead = watchList.Head() {
			listHead.Remove()
			watch := (*Watch)(listHead.GetContainer(unsafe.Offsetof(Watch{}.listNode)))
			*watch = Watch{}
			callback(watch)
		}
	}
}

// Watch ...
type Watch struct {
	listNode  intrusive.ListNode
	file      *file
	eventType EventType
}

// IsReset ...
func (w *Watch) IsReset() bool { return w.listNode.IsReset() }

// EventType ...
type EventType int

// ErrFileNotAttached ...
var ErrFileNotAttached = errors.New("poller: file not attached")

const initialEventBufferLength = 64

type file struct {
	RBTreeNode        intrusive.RBTreeNode
	DirtyListNode     intrusive.ListNode
	FD                int
	WatchLists        [numberOfEventTypes]intrusive.List
	WatchedEventTypes int
}

var filePool = sync.Pool{New: func() interface{} { return new(file) }}

func allocateFile() *file {
	return filePool.Get().(*file)
}

func freeFile(file1 *file) {
	*file1 = file{}
	filePool.Put(file1)
}
