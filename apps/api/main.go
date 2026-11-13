package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/calls"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/mock"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	scenario := flag.String("mock-scenario", "", "run a fictional offline scenario to its first approval boundary, then exit")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *scenario != "" {
		slog.Info("Running fictional mock conversation; stopping before approval and speech", "scenario", *scenario)
		if err := mock.RunConversation(ctx, *scenario, mock.DefaultSeed, os.Stdout); err != nil {
			slog.Error("mock conversation failed")
			os.Exit(1)
		}
		return
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	var store calls.DraftStore
	if databaseURL := os.Getenv("DATABASE_URL"); databaseURL != "" {
		pool, err := pgxpool.New(ctx, databaseURL)
		if err != nil {
			slog.Error("invalid database configuration")
			os.Exit(1)
		}
		defer pool.Close()
		check, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = pool.Ping(check)
		cancel()
		if err != nil {
			slog.Error("call database unavailable")
			os.Exit(1)
		}
		store = repository.New(pool)
	}
	router, err := configuredRouter(os.Getenv("APP_ENV"), store, os.Getenv("APP_ORIGIN"))
	if err != nil {
		slog.Error("invalid API configuration")
		os.Exit(1)
	}
	server := &http.Server{Addr: ":" + port, Handler: router, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			slog.Error("shutdown failed", "error", err)
		}
	}()
	slog.Info("Switchboard listening", "port", port)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}
