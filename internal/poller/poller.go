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
	watcherHashMap   intrusive.HashMap
	dirtyWatcherList intrusive.List
	watcherIDBuffer  int64
	eventsBuffer     []syscall.EpollEvent
}

// Init ...
func (p *Poller) Init() *Poller {
	p.fd = -1

	p.watcherHashMap.Init(
		0,

		func(watcherID interface{}) uint64 {
			return uint64(*watcherID.(*int64)) * 11400714819323198485
		},

		func(hashMapNode *intrusive.HashMapNode, watcherID interface{}) bool {
			watcher := (*watcher)(hashMapNode.GetContainer(unsafe.Offsetof(watcher{}.HashMapNode)))
			return watcher.ID == *watcherID.(*int64)
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
	for {
		it := p.watcherHashMap.Foreach()

		if it.IsAtEnd() {
			break
		}

		watcher := (*watcher)(it.Node().GetContainer(unsafe.Offsetof(watcher{}.HashMapNode)))
		fd := watcher.Fd

		if err := p.doCloseFd(watcher, callback); err != nil {
			log.Printf("geloop.poller WARN: syscall.Close() failed: fd=%d, err=%q", fd, err)
		}
	}

	p.watcherHashMap = intrusive.HashMap{}
	p.dirtyWatcherList = intrusive.List{}
	return syscall.Close(p.fd)
}

// AdoptFd ...
func (p *Poller) AdoptFd(fd int, watcherID int64) {
	watcher := allocateWatcher()
	watcher.ID = watcherID
	watcher.Fd = fd

	for i := range watcher.WatchLists {
		watcher.WatchLists[i].Init()
	}

	p.watcherHashMap.InsertNode(&watcher.HashMapNode, &watcher.ID)
}

// CloseFd ...
func (p *Poller) CloseFd(watcherID int64, callback func(*Watch)) error {
	watcher, err := p.getWatcher(watcherID)

	if err != nil {
		return err
	}

	return p.doCloseFd(watcher, callback)
}

// GetFd ...
func (p *Poller) GetFd(watcherID int64) (int, error) {
	watcher, err := p.getWatcher(watcherID)

	if err != nil {
		return -1, err
	}

	return watcher.Fd, nil
}

// AddWatch ...
func (p *Poller) AddWatch(watch *Watch, watcherID int64, eventType EventType) {
	watcher, _ := p.getWatcher(watcherID)
	watch.watcher = watcher
	watch.eventType = eventType
	watchList := &watcher.WatchLists[eventType-1]
	watchListWasEmpty := watchList.IsEmpty()
	watchList.AppendNode(&watch.listNode)

	if watchListWasEmpty && watcher.DirtyListNode.IsReset() {
		p.dirtyWatcherList.AppendNode(&watcher.DirtyListNode)
	}
}

// RemoveWatch ...
func (p *Poller) RemoveWatch(watch *Watch) {
	watch.listNode.Remove()
	watcher := watch.watcher

	if watcher == nil {
		return
	}

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

	watchList := new(intrusive.List).Init()

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
			p.watcherIDBuffer = eventGetWatcherID(&event)
			hashMapNode, _ := p.watcherHashMap.FindNode(&p.watcherIDBuffer)
			watcher := (*watcher)(hashMapNode.GetContainer(unsafe.Offsetof(watcher{}.HashMapNode)))

			if event.Events&(syscall.EPOLLIN|syscall.EPOLLERR|syscall.EPOLLHUP) != 0 {
				watchList.AppendNodes(&watcher.WatchLists[EventReadable-1])
			}

			if event.Events&(syscall.EPOLLOUT|syscall.EPOLLERR|syscall.EPOLLHUP) != 0 {
				watchList.AppendNodes(&watcher.WatchLists[EventWritable-1])
			}

			p.dirtyWatcherList.AppendNode(&watcher.DirtyListNode)
		}

		if numberOfEvents < len(p.eventsBuffer) {
			break
		}

		p.eventsBuffer = make([]syscall.EpollEvent, 2*len(p.eventsBuffer))
		timeoutMs = 0
	}

	fireWatches(watchList, callback)
	return nil
}

func (p *Poller) getWatcher(watcherID int64) (*watcher, error) {
	p.watcherIDBuffer = watcherID
	hashMapNode, ok := p.watcherHashMap.FindNode(&p.watcherIDBuffer)

	if !ok {
		return nil, ErrInvalidWatcherID
	}

	watcher := (*watcher)(hashMapNode.GetContainer(unsafe.Offsetof(watcher{}.HashMapNode)))
	return watcher, nil
}

func (p *Poller) doCloseFd(watcher *watcher, callback func(*Watch)) error {
	p.watcherHashMap.RemoveNode(&watcher.HashMapNode)

	if !watcher.DirtyListNode.IsReset() {
		watcher.DirtyListNode.Remove()
	}

	fd := watcher.Fd
	watchList := new(intrusive.List).Init()

	for i := range watcher.WatchLists {
		watchList.AppendNodes(&watcher.WatchLists[i])
	}

	if watcher.WatchedEventTypes != 0 {
		if err := syscall.EpollCtl(p.fd, syscall.EPOLL_CTL_DEL, watcher.Fd, nil); err != nil {
			log.Printf("geloop.poller WARN: syscall.EpollCtl() failed: fd=%d, err=%q", watcher.Fd, err)
		}
	}

	freeWatcher(watcher)
	fireWatches(watchList, callback)
	return syscall.Close(fd)
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

			eventSetWatcherID(&event, watcher.ID)
			event.Events = 0

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

// ErrInvalidWatcherID ...
var ErrInvalidWatcherID = errors.New("poller: invalid watcher id")

const initialEventBufferLength = 64

type watcher struct {
	HashMapNode       intrusive.HashMapNode
	DirtyListNode     intrusive.ListNode
	ID                int64
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

func fireWatches(watchList *intrusive.List, callback func(*Watch)) {
	for it := watchList.Foreach(); !it.IsAtEnd(); it.Advance() {
		watch := (*Watch)(it.Node().GetContainer(unsafe.Offsetof(Watch{}.listNode)))
		watch.watcher = nil
	}

	for listHead := watchList.Head(); !listHead.IsNull(watchList); listHead = watchList.Head() {
		listHead.Remove()
		watch := (*Watch)(listHead.GetContainer(unsafe.Offsetof(Watch{}.listNode)))
		*watch = Watch{}
		callback(watch)
	}
}

func eventSetWatcherID(e *syscall.EpollEvent, watcherID int64) {
	e.Fd = int32(watcherID >> 32)
	e.Pad = int32(watcherID)
}

func eventGetWatcherID(e *syscall.EpollEvent) int64 {
	return (int64(e.Fd) << 32) | int64(e.Pad)
}
