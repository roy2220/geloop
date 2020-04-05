package geloop_test

import (
	"net"
	"sync"
	"syscall"
	"testing"

	"github.com/roy2220/geloop"
	"github.com/stretchr/testify/assert"
)

func TestLoopAcceptSockets(t *testing.T) {
	l := new(geloop.Loop).Init()
	err := l.Open()
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	err2 := make(chan error, 1)
	rid, err := l.AcceptSockets(&geloop.AcceptSocketsRequest{
		NetworkName:   "tcp4",
		ListenAddress: "127.0.0.1:8888",
		Callback: func(request *geloop.AcceptSocketsRequest, err error, fd int) {
			if err == nil {
				err = syscall.Close(fd)
				assert.NoError(t, err, "fd=%d", fd)
			} else {
				err2 <- err
				close(err2)
				l.Stop()
			}
		},
	})
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	go func() {
		wg := sync.WaitGroup{}
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(i int) {
				c, err := net.Dial("tcp4", "127.0.0.1:8888")
				if assert.NoError(t, err) {
					c.Close()
				}
				wg.Done()
			}(i)
		}
		wg.Wait()
		l.CancelRequest(rid)
	}()
	l.Run()
	assert.EqualError(t, <-err2, geloop.ErrRequestCanceled.Error())
	err = l.Close()
	assert.NoError(t, err)
}
