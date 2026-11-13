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

	"github.com/andrewkilp/CSC4330_Project6/packages/go/auth"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/handlers"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/providers"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/session"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/mock"
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
	server := &http.Server{Addr: ":" + port, Handler: newRouter(os.Getenv("APP_ENV")), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
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

func newRouter(environment string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", handlers.NewRouter())
	secure := environment != "" && environment != "development"
	session.Register(mux, auth.NewSessions(), secure)
	// Readiness reports configuration presence only. No real telephony adapter is in
	// this build yet (#11), so real calling stays unavailable and mode stays mock.
	readiness := providers.New(providers.FromEnv(os.LookupEnv, providers.Implemented{STT: true, LLM: true, TTS: true}), nil)
	providers.Register(mux, readiness)
	return mux
}
