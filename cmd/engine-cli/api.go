package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/dj/engine-cli/internal/api"
	"github.com/spf13/cobra"
)

const defaultAPIPort = 8787

func newAPICommand() *cobra.Command {
	group := &cobra.Command{Use: "api", Short: "Access your library through a local HTTP API"}
	var port int
	var origins []string
	serve := &cobra.Command{
		Use: "serve", Short: "Start the HTTP API on this machine",
		Long: "Expose all library, playlist, and track operations on 127.0.0.1.\nJobs report progress through JSON status and server-sent events.\nClose Engine DJ before changing or restoring your library.\nJob status and progress are cleared when this server stops. Library undo history is stored separately.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return serveAPI(cmd.Context(), api.Config{LibraryDir: libDirFlag, NoBackup: noBackupFlag, AllowedOrigins: origins}, port, cmd.OutOrStdout())
		},
	}
	serve.Flags().IntVar(&port, "port", defaultAPIPort, "Local HTTP port (0 selects an available port)")
	serve.Flags().StringSliceVar(&origins, "allow-origin", nil, "Exact browser origins permitted to access the API, such as http://localhost:3000")
	group.AddCommand(serve)
	return group
}

func serveAPI(ctx context.Context, config api.Config, port int, out io.Writer) error {
	if port < 0 || port > 65535 {
		return fmt.Errorf("port must be between 0 and 65535")
	}
	app, err := api.New(config)
	if err != nil {
		return err
	}
	defer app.Close()
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("starting HTTP API: %w", err)
	}
	defer listener.Close()
	if _, err := fmt.Fprintf(out, "http://%s\n", listener.Addr()); err != nil {
		return err
	}
	server := &http.Server{Handler: app, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	stop := context.AfterFunc(ctx, func() { _ = server.Close() })
	defer stop()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
