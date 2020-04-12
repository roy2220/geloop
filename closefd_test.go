package geloop_test

import (
	"testing"

	"github.com/roy2220/geloop"
	"github.com/stretchr/testify/assert"
)

func TestLoopCloseFd(t *testing.T) {
	l := new(geloop.Loop).Init(0)
	err := l.Open()
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	l.AdoptFd(&geloop.AdoptFdRequest{Fd: 100})
	{
		var err error
		l.CloseFd(&geloop.CloseFdRequest{
			Fd: 100,
			Callback: func(_ *geloop.CloseFdRequest, err2 error) {
				err = err2
				l.Stop()
			},
		})
		l.Run()
		assert.Error(t, err)
	}
	{
		var err error
		l.CloseFd(&geloop.CloseFdRequest{
			Fd: 100,
			Callback: func(_ *geloop.CloseFdRequest, err2 error) {
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
		l.CloseFd(&geloop.CloseFdRequest{
			Fd: 100,
			Callback: func(_ *geloop.CloseFdRequest, err2 error) {
				err <- err2
			},
		})
		assert.EqualError(t, <-err, geloop.ErrClosed.Error())
	}
}
