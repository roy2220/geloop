package geloop_test

import (
	"syscall"
	"testing"
	"time"

	"github.com/roy2220/geloop"
	"github.com/stretchr/testify/assert"
)

func TestLoopWriteFile(t *testing.T) {
	l := new(geloop.Loop).Init(0)
	err := l.Open()
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	var fds [2]int
	err = syscall.Pipe2(fds[:], syscall.O_CLOEXEC)
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	err = syscall.SetNonblock(fds[1], true)
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	_, err = syscall.Write(fds[1], make([]byte, 1024*1024))
	if !assert.NoError(t, err) {
		t.FailNow()
	}

	go func() {
		err := make(chan error, 1)
		l.WriteFile(&geloop.WriteFileRequest{
			PreCallback: func(_ *geloop.WriteFileRequest, err2 error, buffer *[]byte) int {
				*buffer = []byte{0}
				return 1
			},
			PostCallback: func(_ *geloop.WriteFileRequest, err2 error, numberOfBytesSent int) {
				err <- err2
			},
		})
		assert.EqualError(t, <-err, geloop.ErrInvalidWatcherID.Error())
		l.Stop()
	}()
	l.Run()

	go func() {
		fd, err2 := syscall.Dup(fds[1])
		if !assert.NoError(t, err2) {
			t.FailNow()
		}
		err := make(chan error, 1)
		var watcherID int64
		l.AdoptFd(&geloop.AdoptFdRequest{
			Fd: fd,
			Callback: func(_ *geloop.AdoptFdRequest, err2 error, watcherID2 int64) {
				watcherID = watcherID2
				err <- err2
			},
		})
		assert.NoError(t, <-err)
		l.WriteFile(&geloop.WriteFileRequest{
			WatcherID: watcherID,
			Deadline:  time.Now().Add(time.Second),
			PreCallback: func(_ *geloop.WriteFileRequest, err2 error, buffer *[]byte) int {
				*buffer = []byte{0}
				return 1
			},
			PostCallback: func(_ *geloop.WriteFileRequest, err2 error, numberOfBytesSent int) {
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
			Fd: fds[1],
			Callback: func(_ *geloop.AdoptFdRequest, err2 error, watcherID2 int64) {
				watcherID = watcherID2
				err <- err2
			},
		})
		assert.NoError(t, <-err)
		l.WriteFile(&geloop.WriteFileRequest{
			WatcherID: watcherID,
			Deadline:  time.Now().Add(time.Second / 2),
			PreCallback: func(_ *geloop.WriteFileRequest, err2 error, buffer *[]byte) int {
				*buffer = []byte{0}
				return 1
			},
			PostCallback: func(_ *geloop.WriteFileRequest, err2 error, numberOfBytesSent int) {
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
		err := make(chan error, 2)
		rid := l.WriteFile(&geloop.WriteFileRequest{
			WatcherID: watcherID,
			Deadline:  time.Now().Add(time.Second),
			PreCallback: func(_ *geloop.WriteFileRequest, err2 error, buffer *[]byte) int {
				err <- err2
				*buffer = make([]byte, 1024*1024)
				return len(*buffer)
			},
			PostCallback: func(_ *geloop.WriteFileRequest, err2 error, n int) {
				err <- err2
				return
			},
		})
		syscall.Read(fds[0], make([]byte, 4096))
		assert.NoError(t, <-err)
		go func() {
			l.CancelRequest(rid)
			l.CancelRequest(rid)
		}()
		assert.EqualError(t, <-err, geloop.ErrRequestCanceled.Error())
		l.Stop()
	}()
	l.Run()

	go func() {
		err := make(chan error, 2)
		l.WriteFile(&geloop.WriteFileRequest{
			WatcherID: watcherID,
			Deadline:  time.Now().Add(time.Second),
			PreCallback: func(_ *geloop.WriteFileRequest, err2 error, buffer *[]byte) int {
				err <- err2
				*buffer = []byte{0}
				return 1
			},
			PostCallback: func(_ *geloop.WriteFileRequest, err2 error, n int) {
				err <- err2
				assert.Equal(t, 1, n)
				return
			},
		})
		go func() {
			time.Sleep(time.Second / 2)
			_, err := syscall.Read(fds[0], make([]byte, 1024*1024))
			assert.NoError(t, err)
			_, err = syscall.Read(fds[0], make([]byte, 1024*1024))
			assert.NoError(t, err)
		}()
		assert.NoError(t, <-err)
		assert.NoError(t, <-err)
		l.Stop()
	}()
	l.Run()

	go func() {
		data := make([]byte, 1024*1024)
		for i := range data {
			data[i] = uint8(i % 256)
		}
		err := make(chan error, 2)
		l.WriteFile(&geloop.WriteFileRequest{
			WatcherID: watcherID,
			PreCallback: func(_ *geloop.WriteFileRequest, err2 error, buffer *[]byte) int {
				err <- err2
				*buffer = data
				return len(data)
			},
			PostCallback: func(_ *geloop.WriteFileRequest, err2 error, n int) {
				err <- err2
				assert.Equal(t, len(data), n)
				return
			},
		})
		go func() {
			buf := make([]byte, len(data))
			i := 0
			for {
				n, err := syscall.Read(fds[0], buf[i:])
				if !assert.NoError(t, err) {
					t.FailNow()
				}
				if n == 0 {
					break
				}
				i += n
			}
			assert.Equal(t, data, buf, "%q ------ %q", data[:100], buf[:100])
		}()
		assert.NoError(t, <-err)
		assert.NoError(t, <-err)
		l.Stop()
	}()
	l.Run()

	go func() {
		data := make([]byte, 1024*1024)
		for i := range data {
			data[i] = uint8(i % 256)
		}
		err := make(chan error, 2)
		l.WriteFile(&geloop.WriteFileRequest{
			WatcherID: watcherID,
			Deadline:  time.Now().Add(time.Second / 2),
			PreCallback: func(_ *geloop.WriteFileRequest, err2 error, buffer *[]byte) int {
				err <- err2
				*buffer = data
				return len(data)
			},
			PostCallback: func(_ *geloop.WriteFileRequest, err2 error, n int) {
				err <- err2
				assert.GreaterOrEqual(t, n, len(data)/2)
				return
			},
		})
		go func() {
			buf := make([]byte, len(data)/2)
			i := 0
			for {
				n, err := syscall.Read(fds[0], buf[i:])
				if !assert.NoError(t, err) {
					t.FailNow()
				}
				if n == 0 {
					break
				}
				i += n
			}
		}()
		assert.NoError(t, <-err)
		assert.EqualError(t, <-err, geloop.ErrDeadlineReached.Error())
		l.Stop()
	}()
	l.Run()

	err = l.Close()
	if !assert.NoError(t, err) {
		t.FailNow()
	}
}
