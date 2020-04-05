package geloop_test

import (
	"testing"

	"github.com/roy2220/geloop"
	"github.com/stretchr/testify/assert"
)

func TestLoopDetachFile(t *testing.T) {
	l := new(geloop.Loop).Init()
	err := l.Open()
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	l.AttachFile(&geloop.AttachFileRequest{FD: 0})
	{
		var err error
		var ok bool
		l.DetachFile(&geloop.DetachFileRequest{
			FD: 0,
			Callback: func(_ *geloop.DetachFileRequest, err2 error, ok2 bool) {
				ok = ok2
				err = err2
				l.Stop()
			},
		})
		l.Run()
		assert.NoError(t, err)
		assert.True(t, ok)
	}
	{
		var err error
		var ok bool
		l.DetachFile(&geloop.DetachFileRequest{
			FD: 0,
			Callback: func(_ *geloop.DetachFileRequest, err2 error, ok2 bool) {
				ok = ok2
				err = err2
			},
		})
		err2 := l.Close()
		if !assert.NoError(t, err2) {
			t.FailNow()
		}
		assert.EqualError(t, err, geloop.ErrClosed.Error())
		assert.False(t, ok)
	}
	{
		err := make(chan error, 1)
		var ok bool
		l.DetachFile(&geloop.DetachFileRequest{
			FD: 0,
			Callback: func(_ *geloop.DetachFileRequest, err2 error, ok2 bool) {
				ok = ok2
				err <- err2
			},
		})
		assert.EqualError(t, <-err, geloop.ErrClosed.Error())
		assert.False(t, ok)
	}
}
