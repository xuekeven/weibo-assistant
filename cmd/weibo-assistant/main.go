package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/xuekeven/weibo-assistant/internal/app"
	"github.com/xuekeven/weibo-assistant/internal/config"
)

func main() {
	// main 只负责把错误转换成进程退出码，实际启动逻辑放在 run 中。
	if err := run(); err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	// 所有子命令共用一份配置和日志格式。
	cfg, err := config.Load(".env")
	if err != nil {
		return err
	}
	configureLogging(cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 不带参数时默认启动 API 服务。
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
	case "monitor":
		if err := cfg.ValidateMonitor(); err != nil {
			return err
		}
		return app.RunMonitor(ctx, cfg)
	case "frontend":
		if err := cfg.ValidateFrontend(); err != nil {
			return err
		}
		return app.RunFrontend(ctx, cfg)
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

func configureLogging(value string) {
	// 把 .env 中易读的字符串转换为 slog 使用的日志级别。
	var level slog.Level
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
}

func printUsage() {
	fmt.Println(`Usage:
  weibo-assistant server    Start API and realtime WebSocket server
  weibo-assistant monitor   Run monitor worker and handle QR-code login
  weibo-assistant frontend  Serve the built Vue frontend
  weibo-assistant migrate   Apply database migrations`)
}
