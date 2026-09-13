package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"market-data/internal/bootstrap"
	"market-data/internal/config"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Environ(), os.Stderr, logger, func(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
		return bootstrap.Run(ctx, cfg, logger)
	}); err != nil {
		logger.Error("Service stopped with an error", "error", err)
		os.Exit(1)
	}
}

type startFunc func(context.Context, config.Config, *slog.Logger) error

func run(ctx context.Context, args, environment []string, output io.Writer, logger *slog.Logger, start startFunc) error {
	flags := flag.NewFlagSet("market-data-service", flag.ContinueOnError)
	flags.SetOutput(output)
	path := flags.String("config", "", "YAML configuration file (optional)")
	check := flags.Bool("check-config", false, "Validate configuration and exit")
	health := flags.Bool("healthcheck", false, "Check local HTTP health and exit")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return err
	}

	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}

	if *check && *health {
		return errors.New("check-config and healthcheck cannot be combined")
	}

	var source io.Reader
	if *path != "" {
		file, err := os.Open(*path)
		if err != nil {
			return fmt.Errorf("open configuration: %w", err)
		}

		defer func() { _ = file.Close() }()
		source = file
	}

	cfg, err := config.Load(source, environment)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	if *check {
		logger.Info("Configuration is valid")

		return nil
	}

	if *health {
		return checkHealth(ctx, cfg.Server.HTTP.Host, cfg.Server.HTTP.Port)
	}

	return start(ctx, cfg, logger)
}
