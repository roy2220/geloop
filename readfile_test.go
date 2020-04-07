package geloop_test

import (
	"syscall"
	"testing"
	"time"

	"github.com/roy2220/geloop"
	"github.com/stretchr/testify/assert"
)

func TestLoopReadFile(t *testing.T) {
	l := new(geloop.Loop).Init()
	err := l.Open()
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	var fds [2]int
	err = syscall.Pipe2(fds[:], syscall.O_CLOEXEC)
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	err = syscall.SetNonblock(fds[0], true)
	if !assert.NoError(t, err) {
		t.FailNow()
	}

	go func() {
		err := make(chan error, 1)
		l.ReadFile(&geloop.ReadFileRequest{
			FD: fds[0],
			Callback: func(_ *geloop.ReadFileRequest, err2 error, data []byte, preBuffer []byte) bool {
				err <- err2
				return false
			},
		})
		assert.EqualError(t, <-err, geloop.ErrFileNotAttached.Error())
		l.Stop()
	}()
	l.Run()

	go func() {
		err := make(chan error, 1)
		l.AttachFile(&geloop.AttachFileRequest{
			FD: fds[0],
			Callback: func(_ *geloop.AttachFileRequest, err2 error, _ bool) {
				err <- err2
			},
		})
		assert.NoError(t, <-err)
		l.ReadFile(&geloop.ReadFileRequest{
			FD:       fds[0],
			Deadline: time.Now().Add(time.Second),
			Callback: func(_ *geloop.ReadFileRequest, err2 error, data []byte, preBuffer []byte) bool {
				err <- err2
				return false
			},
		})
		go func() {
			time.Sleep(time.Second / 2)
			l.DetachFile(&geloop.DetachFileRequest{
				FD:       fds[0],
				Callback: func(_ *geloop.DetachFileRequest, err2 error, _ bool) {},
			})
		}()
		assert.EqualError(t, <-err, geloop.ErrFileDetached.Error())
		l.AttachFile(&geloop.AttachFileRequest{
			FD: fds[0],
			Callback: func(_ *geloop.AttachFileRequest, err2 error, _ bool) {
				err <- err2
			},
		})
		assert.NoError(t, <-err)
		l.Stop()
	}()
	l.Run()

	go func() {
		err := make(chan error, 1)
		l.ReadFile(&geloop.ReadFileRequest{
			FD:       fds[0],
			Deadline: time.Now().Add(time.Second / 2),
			Callback: func(_ *geloop.ReadFileRequest, err2 error, data []byte, preBuffer []byte) bool {
				err <- err2
				return false
			},
		})
		t0 := time.Now()
		assert.EqualError(t, <-err, geloop.ErrDeadlineReached.Error())
		assert.Greater(t, int64(time.Since(t0)+100*time.Millisecond), int64(time.Second/2))
		l.Stop()
	}()
	l.Run()

	go func() {
		buf := make([]byte, 4096)
		for i := range buf {
			buf[i] = uint8(i % 256)
		}
		err := make(chan error, 1)
		l.ReadFile(&geloop.ReadFileRequest{
			FD:       fds[0],
			Deadline: time.Now().Add(time.Second),
			Callback: func(_ *geloop.ReadFileRequest, err2 error, data []byte, preBuffer []byte) bool {
				err <- err2
				assert.Equal(t, buf, data)
				assert.GreaterOrEqual(t, len(preBuffer), len(data))
				return false
			},
		})
		go func() {
			time.Sleep(time.Second / 2)
			n, err := syscall.Write(fds[1], buf)
			if assert.NoError(t, err) {
				assert.Equal(t, len(buf), n)
			}
		}()
		assert.NoError(t, <-err)
		l.Stop()
	}()
	l.Run()

	go func() {
		err := make(chan error, 1)
		rid := l.ReadFile(&geloop.ReadFileRequest{
			FD:       fds[0],
			Deadline: time.Now().Add(time.Second),
			Callback: func(_ *geloop.ReadFileRequest, err2 error, data2 []byte, preBuffer []byte) bool {
				err <- err2
				return true
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
		buf := make([]byte, 1024*1024)
		for i := range buf {
			buf[i] = uint8(i % 256)
		}
		data := []byte(nil)
		err := make(chan error, 1)
		l.ReadFile(&geloop.ReadFileRequest{
			FD:       fds[0],
			Deadline: time.Now().Add(time.Second),
			Callback: func(_ *geloop.ReadFileRequest, err2 error, data2 []byte, preBuffer []byte) bool {
				if err2 != nil {
					err <- err2
				}
				data = append(data, data2...)
				assert.GreaterOrEqual(t, len(preBuffer), len(data2))
				return true
			},
		})
		go func() {
			time.Sleep(time.Second / 2)
			n, err := syscall.Write(fds[1], buf)
			if assert.NoError(t, err) {
				assert.Equal(t, len(buf), n)
				err = syscall.Close(fds[1])
				assert.NoError(t, err)
			}
		}()
		assert.EqualError(t, <-err, geloop.ErrNoMoreData.Error())
		assert.Equal(t, buf, data)
		l.Stop()
	}()
	l.Run()

	err = l.Close()
	if !assert.NoError(t, err) {
		t.FailNow()
	}
}
