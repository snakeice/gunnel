package cmd

import (
	"context"
	"net/http"
	_ "net/http/pprof"

	"github.com/sirupsen/logrus"
	"github.com/snakeice/gunnel/pkg/client"
	"github.com/snakeice/gunnel/pkg/signal"
	"github.com/spf13/cobra"
)

func AddClientCmd(rootCmd *cobra.Command) error {
	var configFile string
	var pprofAddr string

	var clientCmd = &cobra.Command{
		Use:   "client",
		Short: "Run the tunnel client",
		Long: `Run the tunnel client that connects to a server and exposes a local port.
The client supports both HTTP and TCP protocols.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runClient(configFile, pprofAddr)
		},
	}

	clientCmd.Flags().
		StringVarP(&configFile, "config", "c", "gunnel.yaml", "Path to the client config file")
	clientCmd.Flags().
		StringVar(&pprofAddr, "pprof", "", "pprof address (e.g. localhost:6061), empty to disable")

	rootCmd.AddCommand(clientCmd)

	return nil
}

func runClient(configFile, pprofAddr string) error {
	if pprofAddr != "" {
		go func() {
			logrus.Infof("Starting pprof server on %s", pprofAddr)
			if err := http.ListenAndServe(pprofAddr, nil); err != nil {
				logrus.WithError(err).Error("pprof server failed")
			}
		}()
	}

	logrus.WithField("config", configFile).Info("Loading client config")

	clientConfig, err := client.LoadConfig(configFile)
	if err != nil {
		logrus.WithError(err).Error("Failed to load client config")
		return nil
	}

	logrus.Info("Starting client mode")

	cm, err := client.New(clientConfig)

	if err != nil {
		logrus.WithError(err).Error("Failed to create connection manager")
		return nil
	}

	if err := cm.Start(context.Background()); err != nil {
		logrus.WithError(err).Error("Failed to start client")
		return nil
	}

	signal.WaitInterruptSignal()

	return nil
}
