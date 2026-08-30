// Command kilasflow runs the embeddable workflow automation engine: REST API,
// webhook server, and the editor SPA, from a single binary.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/kilaslabs/kilas-flow/internal/api"
	"github.com/kilaslabs/kilas-flow/internal/api/handlers"
	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/nodes"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "0.1.0-dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "kilasflow: %v\n", err)
		os.Exit(1)
	}
}

// run wires the application from constructors, so every dependency is explicit
// and there is no global service locator to unpick later.
func run() error {
	configPath := flag.String("config", "config.yaml", "path to the configuration file")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return nil
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	log := newLogger(cfg.Log)
	log.Info("starting kilasflow", "version", version)

	// Cancelled on SIGINT/SIGTERM, which unwinds the server and the database.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(ctx, cfg.Database, log)
	if err != nil {
		return err
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Error("closing database", "error", err)
		}
	}()
	if err := migrate(db); err != nil {
		return err
	}
	nodeRegistry := node.NewRegistry()
	if err := nodes.RegisterAll(nodeRegistry); err != nil {
		return fmt.Errorf("register built-in nodes: %w", err)
	}

	server := api.NewServer(api.Deps{
		Config:       cfg,
		Logger:       log,
		DB:           handlers.Pinger(db),
		NodeRegistry: nodeRegistry,
		Workflows:    repository.NewWorkflowStore(db.DB),
		Executions:   repository.NewExecutionStore(db.DB),
		Version:      version,
	})

	return server.Run(ctx)
}

func migrate(db *database.DB) error {
	return database.Migrate(db, repository.Models()...)
}

// newLogger builds the structured logger described by the configuration.
func newLogger(cfg config.Log) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(cfg.Level)}

	var handler slog.Handler
	if cfg.Format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}

func parseLevel(level string) slog.Level {
	var parsed slog.Level
	if err := parsed.UnmarshalText([]byte(level)); err != nil {
		return slog.LevelInfo
	}

	return parsed
}
