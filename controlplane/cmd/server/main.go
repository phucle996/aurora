package main

import (
	"controlplane/pkg/logger"
	"os"
	"os/signal"
	"syscall"
	"time"

	"controlplane/internal/app"
	"controlplane/internal/config"
)

func main() {
	cfg := config.LoadConfig()

	loc, err := time.LoadLocation(cfg.App.TimeZone)
	if err != nil {
		logger.SysWarn("main", "Failed to load timezone from environment variable "+cfg.App.TimeZone+": "+err.Error())
		time.Local = time.UTC
	} else {
		time.Local = loc
	}

	logger.InitLogger()

	application, err := app.NewApplication(cfg)
	if err != nil {
		logger.SysFatal("main", "Failed to initialize application: "+err.Error())
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := application.Start(); err != nil {
			logger.SysFatal("main", "Application failed to start: "+err.Error())
		}
	}()

	<-stop

	application.Stop()
	logger.SysInfo("main", "Application stopped gracefully.")
}
