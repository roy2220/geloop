package geloop_test

import (
	"testing"

	"github.com/roy2220/geloop"
	"github.com/stretchr/testify/assert"
)

func TestLoopAttachFile(t *testing.T) {
	l := new(geloop.Loop).Init()
	err := l.Open()
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	{
		var err error
		var ok bool
		l.AttachFile(&geloop.AttachFileRequest{
			FD: 0,
			Callback: func(_ *geloop.AttachFileRequest, err2 error, ok2 bool) {
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
		l.AttachFile(&geloop.AttachFileRequest{
			FD: 0,
			Callback: func(_ *geloop.AttachFileRequest, err2 error, ok2 bool) {
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
		l.AttachFile(&geloop.AttachFileRequest{
			FD: 0,
			Callback: func(_ *geloop.AttachFileRequest, err2 error, ok2 bool) {
				ok = ok2
				err <- err2
			},
		})
		assert.EqualError(t, <-err, geloop.ErrClosed.Error())
		assert.False(t, ok)
	}
}
