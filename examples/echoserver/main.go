package main

import (
	"context"
	"flag"
	"fmt"
	"runtime"
	"strconv"

	// "net/http"
	// _ "net/http/pprof"
	"os"
	"os/signal"
	"unsafe"

	"github.com/roy2220/geloop"
	"github.com/roy2220/geloop/byteslicepool"
)

var numLoop int
var listenPort int

func init() {
	flag.IntVar(&numLoop, "n", runtime.NumCPU(), "number of loops")
	flag.IntVar(&listenPort, "l", 8888, "listen port")
	flag.Parse()
}

func main() {
	// go func() {
	//	fmt.Println(http.ListenAndServe("127.0.0.1:3389", nil))
	// }()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		c := make(chan os.Signal)
		signal.Notify(c, os.Interrupt)
		<-c
		cancel()
	}()
	runEchoServer(ctx)
}

func runEchoServer(ctx context.Context) {
	lg := new(geloop.LoopGroup).Init(numLoop, 0)
	if err := lg.Open(); err != nil {
		panic(err)
	}

	s := lg.InitServer(&geloop.Server{
		URL: "tcp://0.0.0.0:" + strconv.Itoa(listenPort),
		OnSetup: func(s *geloop.Server) {
			fmt.Printf("listening on %s ...\n", s.URL)
		},
		OnConnection: func(_ *geloop.Server, fd int) {
			handleConn(lg, fd)
		},
		OnShutdown: func(s *geloop.Server) {
			fmt.Println("exit")
		},
	})

	if err := s.Add(); err != nil {
		panic(err)
	}

	go func() {
		<-ctx.Done()
		lg.Stop()
	}()
	lg.Run()
	lg.Close()
}

func handleConn(lg *geloop.LoopGroup, fd int) {
	lg.AdoptFd(&geloop.AdoptFdRequest{
		Fd:             fd,
		CloseFdOnError: true,
		Callback: func(_ *geloop.AdoptFdRequest, err error, watcherID int64) {
			if err != nil {
				return
			}
			fmt.Printf("new conn fd=%d\n", fd)
			c := conn{
				lg:        lg,
				watcherID: watcherID,
			}
			c.read()
		},
	})
}

type conn struct {
	lg               *geloop.LoopGroup
	watcherID        int64
	readFileRequest  geloop.ReadFileRequest
	dataToSent       []byte
	writeFileRequest geloop.WriteFileRequest
}

func (c *conn) read() {
	c.readFileRequest = geloop.ReadFileRequest{
		WatcherID: c.watcherID,
		Callback: func(request *geloop.ReadFileRequest, err error, data []byte, _ []byte) (needMoreData bool) {
			c := (*conn)(unsafe.Pointer(uintptr(unsafe.Pointer(request)) - unsafe.Offsetof(conn{}.readFileRequest)))
			if err != nil {
				c.closeOnError(err)
				return
			}
			dataCopy := append(byteslicepool.Get(), data...) // copy data from shared buffer
			c.write(dataCopy)
			return false
		},
	}
	c.lg.ReadFile(&c.readFileRequest)
}

func (c *conn) write(data []byte) {
	c.dataToSent = data
	c.writeFileRequest = geloop.WriteFileRequest{
		WatcherID: c.watcherID,
		PreCallback: func(request *geloop.WriteFileRequest, err error, buffer *[]byte) (dataSize int) {
			c := (*conn)(unsafe.Pointer(uintptr(unsafe.Pointer(request)) - unsafe.Offsetof(conn{}.writeFileRequest)))
			if err != nil {
				c.closeOnError(err)
				return
			}
			*buffer = append((*buffer)[:0], c.dataToSent...) // put data to shared buffer
			n := len(c.dataToSent)
			byteslicepool.Put(c.dataToSent)
			return n
		},
		PostCallback: func(request *geloop.WriteFileRequest, err error, numberOfBytesSent int) {
			c := (*conn)(unsafe.Pointer(uintptr(unsafe.Pointer(request)) - unsafe.Offsetof(conn{}.writeFileRequest)))
			if err != nil {
				c.closeOnError(err)
				return
			}
			c.read()
		},
	}
	c.lg.WriteFile(&c.writeFileRequest)
}

func (c *conn) closeOnError(err error) {
	if err == geloop.ErrFdClosed {
		return
	}

	fmt.Printf("close conn watcherID=%d err=%q\n", c.watcherID, err)
	c.lg.CloseFd(&geloop.CloseFdRequest{WatcherID: c.watcherID})
}
