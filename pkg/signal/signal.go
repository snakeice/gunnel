package signal

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func WaitInterruptSignal() {
	signalChan := make(chan os.Signal, 1)

	signal.Notify(signalChan,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)

	<-signalChan

	// Perform cleanup actions here
	fmt.Println("Received interrupt signal. Cleaning up...")
}
