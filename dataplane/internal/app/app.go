package app

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"dataplane/internal/config"
)

type App struct {
	cfg    *config.Config
	mu     sync.Mutex
	ready  bool
	closed bool
}

func New(cfg *config.Config) (*App, error) {
	if cfg == nil {
		return nil, fmt.Errorf("app: config is required")
	}
	return &App{cfg: cfg}, nil
}

func (a *App) Start(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return fmt.Errorf("app: already closed")
	}
	a.ready = true
	log.Printf("[%s] dataplane started", a.cfg.App.Name)
	return nil
}

func (a *App) Stop(ctx context.Context) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	a.ready = false
	a.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// future: graceful stop module workers, grpc servers, adapters
		time.Sleep(10 * time.Millisecond)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		log.Printf("[%s] dataplane stopped", a.cfg.App.Name)
		return nil
	}
}
