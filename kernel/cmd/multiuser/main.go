package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/siyuan-note/siyuan/kernel/internal/multiuser"
)

func main() {
	config, err := multiuser.LoadConfig()
	if err != nil {
		slog.Error("invalid multi-user gateway configuration", "error", err)
		os.Exit(1)
	}

	app, err := multiuser.NewApp(context.Background(), config)
	if err != nil {
		slog.Error("initialize multi-user gateway", "error", err)
		os.Exit(1)
	}
	defer app.Close()

	server := app.Server()
	errors := make(chan error, 1)
	go func() {
		errors <- server.ListenAndServe()
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err = <-errors:
		if err != nil {
			slog.Error("gateway stopped", "error", err)
		}
	case <-signals:
		context, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err = server.Shutdown(context); err != nil {
			slog.Error("shutdown gateway", "error", err)
		}
	}
}
