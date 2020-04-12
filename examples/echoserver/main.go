package main

import (
	"context"
	"flag"
	"fmt"
	"strconv"

	// "net/http"
	// _ "net/http/pprof"
	"os"
	"os/signal"
	"unsafe"

	"github.com/roy2220/geloop"
	"github.com/roy2220/geloop/byteslicepool"
)

var listenPort int

func init() {
	flag.IntVar(&listenPort, "p", 8888, "help message for flagname")
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
	loop := new(geloop.Loop).Init(0)
	if err := loop.Open(); err != nil {
		panic(err)
	}

	listenAddress := "0.0.0.0:" + strconv.Itoa(listenPort)
	fmt.Printf("listening on %s ...\n", listenAddress)
	requestID, err := loop.AcceptSockets(&geloop.AcceptSocketsRequest{
		NetworkName:   "tcp4",
		ListenAddress: listenAddress,
		Callback: func(request *geloop.AcceptSocketsRequest, err error, fd int) {
			if err != nil {
				fmt.Printf("accept sockets error: %v", err)
				loop.Stop()
				return
			}
			handleConn(loop, fd)
		},
	})
	if err != nil {
		panic(err)
	}

	go func() {
		<-ctx.Done()
		loop.CancelRequest(requestID)
	}()

	loop.Run()
	loop.Close()
}

func handleConn(loop *geloop.Loop, fd int) {
	fmt.Printf("new conn fd=%d\n", fd)
	c := conn{
		loop: loop,
		fd:   fd,
	}
	c.read()
}

type conn struct {
	loop             *geloop.Loop
	fd               int
	readFileRequest  geloop.ReadFileRequest
	dataToSent       []byte
	writeFileRequest geloop.WriteFileRequest
}

func (c *conn) read() {
	c.readFileRequest = geloop.ReadFileRequest{
		Fd: c.fd,
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
	c.loop.ReadFile(&c.readFileRequest)
}

func (c *conn) write(data []byte) {
	c.dataToSent = data
	c.writeFileRequest = geloop.WriteFileRequest{
		Fd: c.fd,
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
	c.loop.WriteFile(&c.writeFileRequest)
}

func (c *conn) closeOnError(err error) {
	if err == geloop.ErrFdClosed {
		return
	}

	fmt.Printf("close conn fd=%d err=%q\n", c.fd, err)
	c.loop.CloseFd(&geloop.CloseFdRequest{Fd: c.fd})
}
