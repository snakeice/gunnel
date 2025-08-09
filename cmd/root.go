package cmd

import (
	"os"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func Execute() {
	var level string
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		ForceColors:     true,
		TimestampFormat: "2006-01-02 15:04:05",
	})

	rootCmd := &cobra.Command{
		Use:   "gunnel",
		Short: "A lightweight tunneling application",
		Long: `Gunnel is a lightweight tunneling application that supports both HTTP and TCP protocols.
		It allows you to expose local services to the internet through a remote server.`,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			if level != "" {
				lvl, err := logrus.ParseLevel(level)
				if err != nil {
					return err
				}

				logrus.Infof("Setting log level to %s", lvl)

				logrus.SetLevel(lvl)
			}

			return nil
		},
	}

	rootCmd.PersistentFlags().
		StringVarP(&level, "log-level", "l", "debug", "Set the log level (trace, debug, info, warn, error, fatal, panic)")
	if err := rootCmd.PersistentFlags().MarkHidden("log-level"); err != nil {
		logrus.Error(err)
		os.Exit(1)
	}

	if err := AddClientCmd(rootCmd); err != nil {
		logrus.Error(err)
		os.Exit(1)
	}

	if err := AddServerCmd(rootCmd); err != nil {
		logrus.Error(err)
		os.Exit(1)
	}

	if err := rootCmd.Execute(); err != nil {
		logrus.Error(err)
		os.Exit(1)
	}
}
