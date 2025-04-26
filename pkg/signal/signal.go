package signal

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/sirupsen/logrus"
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
	logrus.Info("Received interrupt signal. Cleaning up...")
}
