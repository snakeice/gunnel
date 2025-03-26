package main

import (
	"net"
	"net/http"

	"github.com/sirupsen/logrus"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		logrus.Info("Received request")
		_, _ = w.Write([]byte("{\"message\": \"Hello, world!\"}"))
	})

	listener, err := net.Listen("tcp", "127.0.0.1:3000")
	if err != nil {
		panic(err)
	}
	defer listener.Close()
	if err := http.Serve(listener, nil); err != nil {
		panic(err)
	}
}
