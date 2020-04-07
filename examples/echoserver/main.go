package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/roy2220/geloop"
)

func runEchoServer(ctx context.Context) {
	loop := new(geloop.Loop).Init()
	if err := loop.Open(); err != nil {
		panic(err)
	}

	closeOnError := func(err error, fd int) {
		if err != geloop.ErrFileDetached {
			loop.DetachFile(&geloop.DetachFileRequest{FD: fd, CloseFile: true})
		}
	}
	read := func(fd int, callback func(data []byte)) {
		loop.ReadFile(&geloop.ReadFileRequest{
			FD: fd,
			Callback: func(request *geloop.ReadFileRequest, err error, data []byte, _ []byte) (needMoreData bool) {
				if err != nil {
					closeOnError(err, request.FD)
					return
				}
				dataCopy := make([]byte, len(data))
				copy(dataCopy, data) // copy data from the shared buffer
				callback(dataCopy)
				return false
			},
		})
	}
	write := func(fd int, data []byte, callback func(fd int)) {
		loop.WriteFile(&geloop.WriteFileRequest{
			FD: fd,
			PreCallback: func(request *geloop.WriteFileRequest, err error, buffer *[]byte) (dataSize int) {
				if err != nil {
					closeOnError(err, request.FD)
					return
				}
				*buffer = append((*buffer)[:0], data...) // put data to the shared buffer
				return len(data)
			},
			PostCallback: func(request *geloop.WriteFileRequest, err error, numberOfBytesWritten int) {
				if err != nil {
					closeOnError(err, request.FD)
					return
				}
				callback(request.FD)
			},
		})
	}
	var pipe func(int)
	pipe = func(fd int) {
		read(fd, func(data []byte) {
			write(fd, data, pipe)
		})
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
			pipe(fd)
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

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		c := make(chan os.Signal)
		signal.Notify(c, os.Interrupt)
		<-c
		cancel()
	}()
	runEchoServer(ctx)
}
