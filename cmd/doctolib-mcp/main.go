// Command doctolib-mcp is a read-only, anonymous MCP server for doctolib.fr over stdio.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tio-plato/doctolib-mcp/internal/doctolib"
	"github.com/tio-plato/doctolib-mcp/internal/gate"
	"github.com/tio-plato/doctolib-mcp/internal/place"
	"github.com/tio-plato/doctolib-mcp/internal/tools"
)

func main() {
	err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "doctolib-mcp:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	chrome, err := doctolib.ChromeMajor()
	if err != nil {
		return err
	}
	dir, err := gate.DefaultDir()
	if err != nil {
		return err
	}
	client := doctolib.New(doctolib.Config{
		BaseURL:     doctolib.DefaultBaseURL,
		ChromeMajor: chrome,
		Gate: gate.New(gate.Config{
			Dir: dir, Interval: gate.DefaultInterval, Hourly: gate.DefaultHourly, Daily: gate.DefaultDaily, Now: nil,
		}),
		Timeout: 0,
	})
	t, err := tools.New(client, place.NewResolver(place.DefaultBaseURL), time.Now)
	if err != nil {
		return err
	}
	server, err := tools.NewServer(t, version())
	if err != nil {
		return err
	}
	err = server.Run(ctx, &mcp.StdioTransport{})
	if err != nil {
		return fmt.Errorf("serve MCP over stdio: %w", err)
	}
	return nil
}

func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "devel"
	}
	return info.Main.Version
}
