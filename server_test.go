package geloop_test

import (
	"net"
	"sync"
	"syscall"
	"testing"

	"github.com/roy2220/geloop"
	"github.com/stretchr/testify/assert"
)

func TestServer(t *testing.T) {
	l := new(geloop.Loop).Init(0)
	err := l.Open()
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	const N = 100
	var e, st, sd, cu bool
	var nc int
	s := (&geloop.Server{
		URL: "tcp://127.0.0.1:8888",
		OnError: func(s *geloop.Server, err error) {
			t.Logf("err=%q", err)
			e = true
		},
		OnSetup: func(s *geloop.Server) { st = true },
		OnConnection: func(s *geloop.Server, fd int) {
			syscall.Close(fd)
			nc++
			if nc == N {
				s.Remove()
				s.Remove()
			}
		},
		OnShutdown: func(s *geloop.Server) { sd = true },
		OnCleanup: func(s *geloop.Server) {
			cu = true
			l.Stop()
		},
	}).Init(l)
	err = s.Add()
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	go func() {
		wg := sync.WaitGroup{}
		for i := 0; i < N; i++ {
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
	}()
	l.Run()
	assert.False(t, e)
	assert.True(t, st)
	assert.True(t, sd)
	assert.True(t, cu)
	assert.Equal(t, N, nc)

	err = l.Close()
	if !assert.NoError(t, err) {
		t.FailNow()
	}
}
