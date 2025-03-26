package cmd

import (
	"fmt"

	"github.com/snakeice/gunnel/pkg/server"
	"github.com/spf13/cobra"
)

func AddServerCmd(rootCmd *cobra.Command) error {
	var (
		serverPort     int
		clientPort     int
		serverProtocol string
		webUIPort      int
	)

	var serverCmd = &cobra.Command{
		Use:   "server",
		Short: "Run the tunnel server",
		Long: `Run the tunnel server that accepts connections from clients.
The server supports both HTTP and TCP protocols for local service connections.
Uses separate ports for client-server communication and user connections.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			srv := server.NewServer(
				serverPort,
				clientPort,
				serverProtocol,
				webUIPort,
			)

			// Start HTTP/TCP server for user connections
			if err := srv.Start(cmd.Context()); err != nil {
				return fmt.Errorf("failed to start server: %w", err)
			}

			return nil
		},
	}
	rootCmd.AddCommand(serverCmd)

	serverCmd.Flags().
		IntVarP(&serverPort, "port", "p", 8080, "Port to listen on for user connections")
	serverCmd.Flags().
		IntVarP(&clientPort, "client-port", "c", 8081, "Port to listen on for client connections")
	serverCmd.Flags().
		StringVarP(&serverProtocol, "protocol", "P", "http", "Protocol to use for local service (http or tcp)")
	serverCmd.Flags().IntVarP(&webUIPort, "webui-port", "w", 8082, "Port to listen on for web UI")

	if err := serverCmd.MarkFlagRequired("port"); err != nil {
		return err
	}

	return nil
}
