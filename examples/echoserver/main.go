package main

import (
	"context"
	"fmt"
	// "net/http"
	// _ "net/http/pprof"
	"os"
	"os/signal"
	"unsafe"

	"github.com/roy2220/geloop"
	"github.com/roy2220/geloop/byteslicepool"
)

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
	loop := new(geloop.Loop).Init()
	if err := loop.Open(); err != nil {
		panic(err)
	}

	requestID, err := loop.AcceptSockets(&geloop.AcceptSocketsRequest{
		NetworkName:   "tcp4",
		ListenAddress: "127.0.0.1:8888",
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
		FD: c.fd,
		Callback: func(request *geloop.ReadFileRequest, err error, data []byte, _ []byte) (needMoreData bool) {
			c := (*conn)(unsafe.Pointer(uintptr(unsafe.Pointer(request)) - unsafe.Offsetof(conn{}.readFileRequest)))
			if err != nil {
				c.close()
				return
			}
			dataCopy := append(byteslicepool.Get(), data...)
			c.write(dataCopy)
			return false
		},
	}
	c.loop.ReadFile(&c.readFileRequest)
}

func (c *conn) write(data []byte) {
	c.dataToSent = data
	c.writeFileRequest = geloop.WriteFileRequest{
		FD: c.fd,
		PreCallback: func(request *geloop.WriteFileRequest, err error, buffer *[]byte) (dataSize int) {
			c := (*conn)(unsafe.Pointer(uintptr(unsafe.Pointer(request)) - unsafe.Offsetof(conn{}.writeFileRequest)))
			if err != nil {
				c.close()
				return
			}
			*buffer = append((*buffer)[:0], c.dataToSent...) // put data to the shared buffer
			n := len(c.dataToSent)
			byteslicepool.Put(c.dataToSent)
			return n
		},
		PostCallback: func(request *geloop.WriteFileRequest, err error, numberOfBytesSent int) {
			c := (*conn)(unsafe.Pointer(uintptr(unsafe.Pointer(request)) - unsafe.Offsetof(conn{}.writeFileRequest)))
			if err != nil {
				c.close()
				return
			}
			c.read()
		},
	}
	c.loop.WriteFile(&c.writeFileRequest)
}

func (c *conn) close() {
	c.loop.DetachFile(&geloop.DetachFileRequest{FD: c.fd, CloseFile: true})
}
