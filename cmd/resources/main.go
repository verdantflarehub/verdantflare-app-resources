package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/verdantflarehub/verdantflare-app-resources/internal/resources"
	"github.com/verdantflarehub/verdantflare-app-resources/internal/server"
)

func run() int {
	source, err := resources.NewStationSource(os.Getenv("STATION_URL"), os.Getenv("STATION_RESOURCES_TOKEN"), nil)
	if err != nil {
		slog.Error("resources_configuration_invalid")
		return 1
	}
	assets := os.Getenv("RESOURCES_ASSETS_DIR")
	if assets == "" {
		assets = "frontend/dist"
	}
	if _, err := os.Stat(assets + "/index.html"); err != nil {
		slog.Error("resources_assets_unavailable")
		return 1
	}
	handler, err := server.New(os.Getenv("APP_SERVICE_TOKEN"), os.Getenv("STATION_ID"), resources.Service{Source: source}, os.DirFS(assets))
	if err != nil {
		slog.Error("resources_configuration_invalid")
		return 1
	}
	listen := os.Getenv("RESOURCES_LISTEN")
	if listen == "" {
		listen = "0.0.0.0:8000"
	}
	s := &http.Server{Addr: listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		<-ctx.Done()
		stop, release := context.WithTimeout(context.Background(), 10*time.Second)
		defer release()
		_ = s.Shutdown(stop)
	}()
	if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("resources_server_stopped")
		return 1
	}
	return 0
}

func main() { os.Exit(run()) }
