package geloop

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

// LoopGroup represents a group of event loops.
type LoopGroup struct {
	loops []*Loop
	x     uint64
	wg    sync.WaitGroup
}

// Init initializes the group with the given size
// and then returns the group.
func (lg *LoopGroup) Init(size int, reservedReadBufferSize int) *LoopGroup {
	lg.loops = make([]*Loop, size)

	for i := range lg.loops {
		loop := new(Loop).Init(reservedReadBufferSize)
		loop.group(size, i)
		lg.loops[i] = loop
	}

	lg.x = rand.New(rand.NewSource(time.Now().UnixNano())).Uint64()
	return lg
}

// Open opens the group.
func (lg *LoopGroup) Open() error {
	for i, loop := range lg.loops {
		if err := loop.Open(); err != nil {
			for j := i - 1; j >= 0; j-- {
				lg.loops[j].Close()
			}

			return err
		}
	}

	return nil
}

// Close closes the group. All file descriptors adopted in the
// group will automatically be closed at once.
func (lg *LoopGroup) Close() error {
	for _, loop := range lg.loops {
		if err := loop.Close(); err != nil {
			return err
		}
	}

	return nil
}

// Run runs the group.
func (lg *LoopGroup) Run() {
	for _, loop := range lg.loops {
		lg.wg.Add(1)

		go func(loop *Loop) {
			defer lg.wg.Done()
			loop.Run()
		}(loop)
	}

	lg.wg.Wait()
}

// Stop requests to stop the group.
func (lg *LoopGroup) Stop() {
	for _, loop := range lg.loops {
		loop.Stop()
	}
}

// InitServer initializes and returns the server.
func (lg *LoopGroup) InitServer(server *Server) *Server {
	return server.Init(lg.getLoop())
}

// AdoptFd requests to adopt a file descriptor to the group.
func (lg *LoopGroup) AdoptFd(request *AdoptFdRequest) {
	lg.getLoop().AdoptFd(request)
}

// CloseFd requests to close a file descriptor in the group.
func (lg *LoopGroup) CloseFd(request *CloseFdRequest) {
	lg.getLoopByX(uint64(request.WatcherID)).CloseFd(request)
}

// ReadFile requests to read the given file in the group.
// It returns the request id for cancellation.
func (lg *LoopGroup) ReadFile(request *ReadFileRequest) int64 {
	return lg.getLoopByX(uint64(request.WatcherID)).ReadFile(request)
}

// WriteFile requests to write the given file in the group.
// It returns the request id for cancellation.
func (lg *LoopGroup) WriteFile(request *WriteFileRequest) int64 {
	return lg.getLoopByX(uint64(request.WatcherID)).WriteFile(request)
}

// CancelRequest requests to cancel a request with the given id.
func (lg *LoopGroup) CancelRequest(requestID int64) {
	lg.getLoopByX(uint64(requestID)).CancelRequest(requestID)
}

func (lg *LoopGroup) getLoop() *Loop {
	return lg.getLoopByX(atomic.AddUint64(&lg.x, 1))
}

func (lg *LoopGroup) getLoopByX(x uint64) *Loop {
	return lg.loops[x%uint64(len(lg.loops))]
}
