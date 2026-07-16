package main

import (
	"cost-manager/api/internal/app"
	"cost-manager/api/pkg/logger"
)

func main() {
	// Initialize logger
	logger.InitLogger()
	const op = "main"

	application := app.NewApp()

	if err := application.Init(); err != nil {
		logger.SysFatal(op, "Application initialization failed: "+err.Error())
	}

	application.Start()

	application.Wait()

	application.Stop()
}
