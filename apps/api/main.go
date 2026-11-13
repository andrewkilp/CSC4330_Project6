package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/auth"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/calls"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/events"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/handlers"
	callroutes "github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/calls"
	eventroutes "github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/events"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/providers"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/session"
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
	var store *repository.Repository
	if url := os.Getenv("DATABASE_URL"); url != "" {
		pool, err := openPool(ctx, url)
		if err != nil {
			slog.Error("database unavailable; check DATABASE_URL and docker/README.md", "error", err)
			os.Exit(1)
		}
		defer pool.Close()
		store = repository.New(pool)
	} else {
		slog.Info("DATABASE_URL is not set; call routes are disabled (template mode)")
	}
	server := &http.Server{Addr: ":" + port, Handler: newRouter(os.Getenv("APP_ENV"), store), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
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

func openPool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, errors.New("invalid DATABASE_URL")
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, errors.New("database unavailable")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("database unavailable")
	}
	dir := os.Getenv("MIGRATIONS_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "db", "migrations") // moon runs api tasks from apps/api
		if _, err := os.Stat(dir); err != nil {
			dir = filepath.Join("db", "migrations")
		}
	}
	if err := migrate(ctx, pool, dir); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// newRouter composes the API. Without a store it serves the template routes only.
func newRouter(environment string, store *repository.Repository) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", handlers.NewRouter())
	secure := environment != "" && environment != "development"
	sessions := auth.NewSessions()
	session.Register(mux, sessions, secure)
	// Readiness reports configuration presence only. No real telephony adapter is in
	// this build yet (#11), so real calling stays unavailable and mode stays mock.
	readiness := providers.New(providers.FromEnv(os.LookupEnv, providers.Implemented{STT: true, LLM: true, TTS: true}), nil)
	providers.Register(mux, readiness)
	if store != nil {
		hub := events.NewHub(0)
		frame, _ := strconv.Atoi(os.Getenv("MOCK_FRAME_DELAY_MS"))
		perUpdate, _ := strconv.Atoi(os.Getenv("MOCK_FRAMES_PER_UPDATE"))
		if frame <= 0 {
			frame = 20 // real time: one 20 ms frame per 20 ms
		}
		if perUpdate <= 0 {
			perUpdate = 40
		}
		svc, err := calls.New(calls.Config{Repo: store, Hub: hub, RequireReal: readiness.RequireReal,
			Mock: mock.Options{FrameDelay: time.Duration(frame) * time.Millisecond, FramesPerUpdate: perUpdate}})
		if err != nil {
			panic(err)
		}
		callroutes.Register(mux, svc, sessions.RequireOwner)
		if err := eventroutes.Register(mux, eventroutes.Config{Store: store, Hub: hub, RequireOwner: sessions.RequireOwner}); err != nil {
			panic(err)
		}
	}
	var handler http.Handler = mux
	if dir := os.Getenv("WEB_DIR"); dir != "" {
		handler = withWeb(mux, dir)
	}
	if origin := os.Getenv("WEB_ORIGIN"); origin != "" {
		handler = withCORS(handler, origin)
	}
	return handler
}

// withWeb serves the exported web app (expo export) from the same origin, so the
// session cookie needs no cross-origin configuration. Unknown non-API paths fall back
// to index.html for client-side routes such as /calls/{id}.
func withWeb(api http.Handler, dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") || r.URL.Path == "/health" || r.Method != http.MethodGet {
			api.ServeHTTP(w, r)
			return
		}
		path := filepath.Join(dir, filepath.FromSlash(filepath.Clean("/"+r.URL.Path)))
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			if _, err := os.Stat(path + ".html"); err == nil {
				r.URL.Path += ".html"
			} else if r.URL.Path != "/" {
				r.URL.Path = "/"
			}
		}
		files.ServeHTTP(w, r)
	})
}

// withCORS allows one configured development origin (Expo web on another port) to
// call the API with the session cookie.
func withCORS(next http.Handler, origin string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") == origin {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Last-Event-ID")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
