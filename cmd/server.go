package cmd

import (
	"fmt"

	"github.com/snakeice/gunnel/pkg/server"
	"github.com/spf13/cobra"
)

func AddServerCmd(rootCmd *cobra.Command) error {
	var (
		configFile string
	)

	var serverCmd = &cobra.Command{
		Use:   "server",
		Short: "Run the tunnel server",
		Long: `Run the tunnel server that accepts connections from clients.
The server supports both HTTP and TCP protocols for local service connections.
Uses separate ports for client-server communication and user connections.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			config := server.DefaultConfig()
			if configFile != "" {
				err := config.LoadConfig(configFile)
				if err != nil {
					return fmt.Errorf("failed to load config: %w", err)
				}
			}

			srv := server.NewServer(config)

			// Start HTTP/TCP server for user connections
			if err := srv.Start(cmd.Context()); err != nil {
				return fmt.Errorf("failed to start server: %w", err)
			}

			return nil
		},
	}
	rootCmd.AddCommand(serverCmd)

	serverCmd.Flags().
		StringVarP(&configFile, "config", "c", "", "Path to the server configuration file")

	return nil
}
