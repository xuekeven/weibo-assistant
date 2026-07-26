package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/xuekeven/weibo-assistant/internal/config"
)

func RunFrontend(ctx context.Context, cfg config.Config) error {
	distDir, err := filepath.Abs(cfg.FrontendDistDir)
	if err != nil {
		return err
	}
	handler, err := frontendFileHandler(distDir)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              net.JoinHostPort(cfg.FrontendBindAddr, strconv.Itoa(cfg.FrontendPort)),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("frontend static server listening", "addr", server.Addr, "distDir", distDir)
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func frontendFileHandler(distDir string) (http.Handler, error) {
	info, err := os.Stat(distDir)
	if err != nil {
		return nil, fmt.Errorf("frontend dist directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("frontend dist path is not a directory: %s", distDir)
	}
	return http.FileServer(http.Dir(distDir)), nil
}
