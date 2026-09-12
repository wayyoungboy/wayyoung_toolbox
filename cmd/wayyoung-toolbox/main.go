package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/wayyoungboy/wayyoung_toolbox/internal/supervisor"
	"github.com/wayyoungboy/wayyoung_toolbox/internal/webapp"
)

var version = "0.1.0"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	appDir := os.Getenv("TRIM_APPDEST")
	dataDir := os.Getenv("TRIM_PKGVAR")
	if !filepath.IsAbs(appDir) || !filepath.IsAbs(dataDir) {
		return errors.New("飞牛应用目录不可用")
	}
	manager, err := supervisor.New(appDir, dataDir, supervisor.DefaultDefinitions(appDir))
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	manager.Start(ctx)
	defer manager.Close()

	application, err := webapp.New("/app/wayyoung_toolbox", filepath.Join(appDir, "public"), manager)
	if err != nil {
		return err
	}
	socket := filepath.Join(appDir, "app.sock")
	if err := removeStaleSocket(socket); err != nil {
		return err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(socket)
	if err := os.Chmod(socket, 0o600); err != nil {
		return err
	}
	server := &http.Server{
		Handler: application, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10,
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	fmt.Printf("wayyoung_toolbox %s ready\n", version)
	select {
	case <-ctx.Done():
		shutdown, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		_ = server.Shutdown(shutdown)
		return nil
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to replace non-socket path %s", path)
	}
	connection, dialErr := net.DialTimeout("unix", path, 200*time.Millisecond)
	if dialErr == nil {
		connection.Close()
		return errors.New("another toolbox process is already running")
	}
	return os.Remove(path)
}
