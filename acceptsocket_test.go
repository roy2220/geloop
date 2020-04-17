package geloop_test

import (
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/roy2220/geloop"
	"github.com/stretchr/testify/assert"
)

func TestLoopAcceptSocket(t *testing.T) {
	l := new(geloop.Loop).Init(333)
	err := l.Open()
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	fd, err := geloop.Listen("tcp", "127.0.0.1:8888")
	if !assert.NoError(t, err) {
		t.FailNow()
	}

	go func() {
		err := make(chan error, 1)
		l.AcceptSocket(&geloop.AcceptSocketRequest{
			Callback: func(_ *geloop.AcceptSocketRequest, err2 error, newFd int) {
				err <- err2
			},
		})
		assert.EqualError(t, <-err, geloop.ErrInvalidWatcherID.Error())
		l.Stop()
	}()
	l.Run()

	go func() {
		fd2, err2 := syscall.Dup(fd)
		if !assert.NoError(t, err2) {
			t.FailNow()
		}
		err := make(chan error, 1)
		var watcherID int64
		l.AdoptFd(&geloop.AdoptFdRequest{
			Fd: fd2,
			Callback: func(_ *geloop.AdoptFdRequest, err2 error, watcherID2 int64) {
				watcherID = watcherID2
				err <- err2
			},
		})
		assert.NoError(t, <-err)
		l.AcceptSocket(&geloop.AcceptSocketRequest{
			WatcherID: watcherID,
			Callback: func(_ *geloop.AcceptSocketRequest, err2 error, newFd int) {
				err <- err2
			},
		})
		go func() {
			time.Sleep(time.Second / 2)
			l.CloseFd(&geloop.CloseFdRequest{
				WatcherID: watcherID,
			})
		}()
		assert.EqualError(t, <-err, geloop.ErrFdClosed.Error())
		l.Stop()
	}()
	l.Run()

	var watcherID int64
	go func() {
		err := make(chan error, 1)
		l.AdoptFd(&geloop.AdoptFdRequest{
			Fd: fd,
			Callback: func(_ *geloop.AdoptFdRequest, err2 error, watcherID2 int64) {
				watcherID = watcherID2
				err <- err2
			},
		})
		assert.NoError(t, <-err)
		l.AcceptSocket(&geloop.AcceptSocketRequest{
			WatcherID: watcherID,
			Deadline:  time.Now().Add(time.Second / 2),
			Callback: func(_ *geloop.AcceptSocketRequest, err2 error, newFd int) {
				err <- err2
			},
		})
		t0 := time.Now()
		assert.EqualError(t, <-err, geloop.ErrDeadlineReached.Error())
		assert.Greater(t, int64(time.Since(t0)+100*time.Millisecond), int64(time.Second/2))
		l.Stop()
	}()
	l.Run()

	go func() {
		err := make(chan error, 1)
		rid := l.AcceptSocket(&geloop.AcceptSocketRequest{
			WatcherID: watcherID,
			Deadline:  time.Now().Add(time.Second),
			Callback: func(_ *geloop.AcceptSocketRequest, err2 error, newFd int) {
				err <- err2
			},
		})
		go func() {
			time.Sleep(time.Second / 2)
			l.CancelRequest(rid)
			l.CancelRequest(rid)
		}()
		assert.EqualError(t, <-err, geloop.ErrRequestCanceled.Error())
		l.Stop()
	}()
	l.Run()

	go func() {
		err := make(chan error, 1)
		l.AcceptSocket(&geloop.AcceptSocketRequest{
			WatcherID: watcherID,
			Deadline:  time.Now().Add(time.Second),
			Callback: func(_ *geloop.AcceptSocketRequest, err2 error, newFd int) {
				if assert.LessOrEqual(t, 0, newFd) {
					syscall.Close(newFd)
				}
				err <- err2
			},
		})
		go func() {
			time.Sleep(time.Second / 2)
			c, err := net.Dial("tcp", "127.0.0.1:8888")
			assert.NoError(t, err)
			c.Close()
		}()
		assert.NoError(t, <-err)
		l.Stop()
	}()
	l.Run()

	err = l.Close()
	if !assert.NoError(t, err) {
		t.FailNow()
	}
}
