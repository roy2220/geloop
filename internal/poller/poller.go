package poller

import (
	"errors"
	"log"
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
	fd               int
	watcherRBTree    intrusive.RBTree
	dirtyWatcherList intrusive.List
	eventsBuffer     []syscall.EpollEvent
}

// Init ...
func (p *Poller) Init() *Poller {
	p.fd = -1

	p.watcherRBTree.Init(
		func(rbTreeNode1, rbTreeNode2 *intrusive.RBTreeNode) bool {
			watcher1 := (*watcher)(rbTreeNode1.GetContainer(unsafe.Offsetof(watcher{}.RBTreeNode)))
			watcher2 := (*watcher)(rbTreeNode2.GetContainer(unsafe.Offsetof(watcher{}.RBTreeNode)))
			return watcher1.Fd < watcher2.Fd
		},

		func(rbTreeNode1 *intrusive.RBTreeNode, fd interface{}) int64 {
			watcher := (*watcher)(rbTreeNode1.GetContainer(unsafe.Offsetof(watcher{}.RBTreeNode)))
			return int64(watcher.Fd - fd.(int))
		},
	)

	p.dirtyWatcherList.Init()
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
	for rbTreeRoot, ok := p.watcherRBTree.GetRoot(); ok; rbTreeRoot, ok = p.watcherRBTree.GetRoot() {
		p.watcherRBTree.RemoveNode(rbTreeRoot)
		watcher := (*watcher)(rbTreeRoot.GetContainer(unsafe.Offsetof(watcher{}.RBTreeNode)))
		fd := watcher.Fd

		if err := doCloseFd(watcher, callback); err != nil {
			log.Printf("geloop.poller WARN: doCloseFd() failed: fd=%d, err=%q", fd, err)
		}
	}

	p.watcherRBTree = intrusive.RBTree{}
	p.dirtyWatcherList = intrusive.List{}
	return syscall.Close(p.fd)
}

// AdoptFd ...
func (p *Poller) AdoptFd(fd int) {
	if rbTreeNode, ok := p.watcherRBTree.FindNode(fd); ok {
		watcher := (*watcher)(rbTreeNode.GetContainer(unsafe.Offsetof(watcher{}.RBTreeNode)))
		watcher.FdAdoptionCount++
		return
	}

	watcher := allocateWatcher()
	watcher.Fd = fd
	watcher.FdAdoptionCount = 1

	for i := range watcher.WatchLists {
		watcher.WatchLists[i].Init()
	}

	p.watcherRBTree.InsertNode(&watcher.RBTreeNode)
}

// CloseFd ...
func (p *Poller) CloseFd(fd int, callback func(*Watch)) error {
	rbTreeNode, ok := p.watcherRBTree.FindNode(fd)

	if !ok {
		return ErrInvalidFd
	}

	watcher := (*watcher)(rbTreeNode.GetContainer(unsafe.Offsetof(watcher{}.RBTreeNode)))
	watcher.FdAdoptionCount--

	if watcher.FdAdoptionCount >= 1 {
		return nil
	}

	p.watcherRBTree.RemoveNode(rbTreeNode)

	if !watcher.DirtyListNode.IsReset() {
		watcher.DirtyListNode.Remove()
	}

	return doCloseFd(watcher, callback)
}

// AddWatch ...
func (p *Poller) AddWatch(watch *Watch, fd int, eventType EventType) error {
	rbTreeNode, ok := p.watcherRBTree.FindNode(fd)

	if !ok {
		return ErrInvalidFd
	}

	watcher := (*watcher)(rbTreeNode.GetContainer(unsafe.Offsetof(watcher{}.RBTreeNode)))
	watch.watcher = watcher
	watch.eventType = eventType
	watchList := &watcher.WatchLists[eventType-1]
	watchListWasEmpty := watchList.IsEmpty()
	watchList.AppendNode(&watch.listNode)

	if watchListWasEmpty && watcher.DirtyListNode.IsReset() {
		p.dirtyWatcherList.AppendNode(&watcher.DirtyListNode)
	}

	return nil
}

// RemoveWatch ...
func (p *Poller) RemoveWatch(watch *Watch) {
	watch.listNode.Remove()
	watcher := watch.watcher
	watchList := &watcher.WatchLists[watch.eventType-1]
	*watch = Watch{}

	if watchList.IsEmpty() && watcher.DirtyListNode.IsReset() {
		p.dirtyWatcherList.AppendNode(&watcher.DirtyListNode)
	}
}

// ProcessWatches ...
func (p *Poller) ProcessWatches(deadline time.Time, callback func(*Watch)) error {
	p.flushWatchers()
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
			rbTreeNode, ok := p.watcherRBTree.FindNode(int(event.Fd))

			if !ok {
				continue
			}

			watcher := (*watcher)(rbTreeNode.GetContainer(unsafe.Offsetof(watcher{}.RBTreeNode)))
			var watchLists [numberOfEventTypes]intrusive.List

			for i := range watcher.WatchLists {
				watchLists[i].Init()
			}

			if event.Events&syscall.EPOLLIN != 0 {
				watchLists[EventReadable-1].AppendNodes(&watcher.WatchLists[EventReadable-1])
			}

			if event.Events&syscall.EPOLLOUT != 0 {
				watchLists[EventWritable-1].AppendNodes(&watcher.WatchLists[EventWritable-1])
			}

			p.dirtyWatcherList.AppendNode(&watcher.DirtyListNode)
			iterateWatches(&watchLists, callback)
		}

		if numberOfEvents < len(p.eventsBuffer) {
			break
		}

		p.eventsBuffer = make([]syscall.EpollEvent, 2*len(p.eventsBuffer))
		timeoutMs = 0
	}

	return nil
}

func (p *Poller) flushWatchers() {
	for it := p.dirtyWatcherList.Foreach(); !it.IsAtEnd(); it.Advance() {
		watcher := (*watcher)(it.Node().GetContainer(unsafe.Offsetof(watcher{}.DirtyListNode)))
		watcher.DirtyListNode = intrusive.ListNode{}
		oldWatchedEventTypes := watcher.WatchedEventTypes
		watcher.WatchedEventTypes = 0

		for i := range watcher.WatchLists {
			if !watcher.WatchLists[i].IsEmpty() {
				watcher.WatchedEventTypes |= 1 << i
			}
		}

		if watcher.WatchedEventTypes == oldWatchedEventTypes {
			continue
		}

		var op int
		var event syscall.EpollEvent

		if watcher.WatchedEventTypes == 0 {
			op = syscall.EPOLL_CTL_DEL
		} else {
			if oldWatchedEventTypes == 0 {
				op = syscall.EPOLL_CTL_ADD
			} else {
				op = syscall.EPOLL_CTL_MOD
			}

			event.Fd = int32(watcher.Fd)
			event.Events = uint32(syscall.EPOLLET & 0xFFFFFFFF)

			if watcher.WatchedEventTypes&(1<<(EventReadable-1)) != 0 {
				event.Events |= syscall.EPOLLIN
			}

			if watcher.WatchedEventTypes&(1<<(EventWritable-1)) != 0 {
				event.Events |= syscall.EPOLLOUT
			}
		}

		if err := syscall.EpollCtl(p.fd, op, watcher.Fd, &event); err != nil {
			log.Printf("geloop.poller WARN: syscall.EpollCtl() failed: fd=%d, err=%q", watcher.Fd, err)
		}
	}

	p.dirtyWatcherList.Init()
}

// Watch ...
type Watch struct {
	listNode  intrusive.ListNode
	watcher   *watcher
	eventType EventType
}

// IsReset ...
func (w *Watch) IsReset() bool { return w.listNode.IsReset() }

// EventType ...
type EventType int

// ErrInvalidFd ...
var ErrInvalidFd = errors.New("poller: invalid fd")

const initialEventBufferLength = 64

type watcher struct {
	RBTreeNode        intrusive.RBTreeNode
	DirtyListNode     intrusive.ListNode
	Fd                int
	FdAdoptionCount   int
	WatchLists        [numberOfEventTypes]intrusive.List
	WatchedEventTypes int
}

var watcherPool = sync.Pool{New: func() interface{} { return new(watcher) }}

func allocateWatcher() *watcher {
	return watcherPool.Get().(*watcher)
}

func freeWatcher(watcher1 *watcher) {
	*watcher1 = watcher{}
	watcherPool.Put(watcher1)
}

func doCloseFd(watcher *watcher, callback func(*Watch)) error {
	fd := watcher.Fd
	var watchLists [numberOfEventTypes]intrusive.List

	for i := range watcher.WatchLists {
		watchLists[i].Init()
		watchLists[i].AppendNodes(&watcher.WatchLists[i])
	}

	freeWatcher(watcher)
	iterateWatches(&watchLists, callback)
	return syscall.Close(fd)
}

func iterateWatches(watchLists *[numberOfEventTypes]intrusive.List, callback func(*Watch)) {
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
