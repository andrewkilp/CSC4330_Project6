package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/demo"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/handlers"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	address, router := ":"+port, handlers.NewRouter()
	if os.Getenv("DEMO_MODE") == "true" {
		webDir := os.Getenv("DEMO_WEB_DIR")
		if webDir == "" {
			webDir = "../mobile/dist"
		}
		address = "127.0.0.1:" + port
		router = handlers.NewDemoRouter(webDir, demo.New(0))
	}
	server := &http.Server{Addr: address, Handler: router, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			slog.Error("shutdown failed", "error", err)
		}
	}()
	slog.Info("Switchboard listening", "address", address, "demo", os.Getenv("DEMO_MODE") == "true")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}
