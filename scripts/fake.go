package main

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		logrus.Info("Received request")
		time.Sleep(40 * time.Millisecond)
		_, err := w.Write([]byte("{\"message\": \"Hello, world!\"}"))
		if err != nil {
			logrus.WithError(err).Error("Failed to write response")
			return
		}
	})

	lc := &net.ListenConfig{}
	listener, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:3000")
	if err != nil {
		panic(err)
	}
	defer func() {
		err := listener.Close()
		if err != nil {
			logrus.WithError(err).Panic("Failed to close listener")
		}
	}()

	server := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
		ReadTimeout:       5 * time.Second,
		IdleTimeout:       5 * time.Second,
	}

	if err := server.Serve(listener); err != nil {
		panic(err)
	}
}
