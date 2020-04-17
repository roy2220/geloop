package geloop_test

import (
	"testing"

	"github.com/roy2220/geloop"
	"github.com/stretchr/testify/assert"
)

func TestLoopAdoptFd(t *testing.T) {
	l := new(geloop.Loop).Init(0)
	err := l.Open()
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	{
		var err error
		l.AdoptFd(&geloop.AdoptFdRequest{
			Fd: 100,
			Callback: func(_ *geloop.AdoptFdRequest, err2 error, watcherID int64) {
				err = err2
				l.Stop()
			},
		})
		l.Run()
		assert.NoError(t, err)
	}
	{
		var err error
		l.AdoptFd(&geloop.AdoptFdRequest{
			Fd: 100,
			Callback: func(_ *geloop.AdoptFdRequest, err2 error, watcherID int64) {
				err = err2
			},
		})
		err2 := l.Close()
		if !assert.NoError(t, err2) {
			t.FailNow()
		}
		assert.EqualError(t, err, geloop.ErrClosed.Error())
	}
	{
		err := make(chan error, 1)
		r := geloop.AdoptFdRequest{
			Fd:             100,
			CloseFdOnError: true,
			Callback: func(_ *geloop.AdoptFdRequest, err2 error, watcherID int64) {
				err <- err2
			},
		}
		l.AdoptFd(&r)
		assert.EqualError(t, <-err, geloop.ErrClosed.Error())
	}
}
