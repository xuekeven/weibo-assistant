package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/xuekeven/weibo-assistant/internal/app"
	"github.com/xuekeven/weibo-assistant/internal/config"
)

func main() {
	if err := run(); err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(".env")
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	command := "server"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	switch command {
	case "server":
		if err := cfg.ValidateServer(); err != nil {
			return err
		}
		return app.RunServer(ctx, cfg)
	case "login":
		if err := cfg.ValidateBrowser(); err != nil {
			return err
		}
		return app.RunLogin(ctx, cfg)
	case "monitor":
		if err := cfg.ValidateMonitor(); err != nil {
			return err
		}
		return app.RunMonitor(ctx, cfg)
	case "migrate":
		if err := cfg.ValidateDatabase(); err != nil {
			return err
		}
		return app.RunMigrate(ctx, cfg)
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func printUsage() {
	fmt.Println(`Usage:
  weibo-assistant server   Start API server and monitor worker
  weibo-assistant login    Open Chromium with persistent profile for login
  weibo-assistant monitor  Run monitor worker only
  weibo-assistant migrate  Apply database migrations`)
}
