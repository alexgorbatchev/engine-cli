package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/api"
	"github.com/alexgorbatchev/engine-cli/internal/state"
	"github.com/spf13/cobra"
)

const defaultAPIPort = 8787

func newAPICommand() *cobra.Command {
	group := &cobra.Command{Use: "api", Short: "Access your library through a local HTTP API"}
	var port int
	var origins []string
	var libraries []string
	var statePath string
	serve := &cobra.Command{
		Use: "serve", Short: "Start the HTTP API on this machine",
		Long: "Expose all library, playlist, and track operations on 127.0.0.1.\nJobs report progress through JSON status and server-sent events.\nClose Engine DJ before changing or restoring your library.\nJob status and progress are cleared when this server stops. Library undo history is stored separately.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("library") {
				libraries = nil
				for _, path := range strings.Split(os.Getenv("ENGINE_LIBRARY_PATHS"), ";") {
					if path = strings.TrimSpace(path); path != "" {
						libraries = append(libraries, path)
					}
				}
			}
			if statePath == "" {
				var err error
				statePath, err = state.DefaultPath()
				if err != nil {
					return err
				}
			}
			return serveAPI(cmd.Context(), api.Config{StatePath: statePath, LibraryDir: libDirFlag, LibraryPaths: libraries, NoBackup: noBackupFlag, AllowedOrigins: origins}, port, cmd.OutOrStdout())
		},
	}
	serve.Flags().IntVar(&port, "port", defaultAPIPort, "Local HTTP port (0 selects an available port)")
	serve.Flags().StringVar(&statePath, "state-db", "", "Persistent library preferences database (default: XDG state directory)")
	serve.Flags().StringArrayVar(&libraries, "library", nil, "Open an existing library; repeat for multiple paths (overrides semicolon-separated ENGINE_LIBRARY_PATHS)")
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
