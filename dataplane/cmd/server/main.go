package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"dataplane/internal/app"
	"dataplane/internal/config"
)

func main() {
	cfg := config.Load()

	application, err := app.New(cfg)
	if err != nil {
		log.Fatalf("bootstrap app failed: %v", err)
	}

	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := application.Start(rootCtx); err != nil {
		log.Fatalf("start app failed: %v", err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.App.ShutdownTimeout)
	defer shutdownCancel()

	if err := application.Stop(shutdownCtx); err != nil {
		log.Printf("graceful shutdown error: %v", err)
	}

	// give logger flush window if needed in future
	time.Sleep(20 * time.Millisecond)
}
