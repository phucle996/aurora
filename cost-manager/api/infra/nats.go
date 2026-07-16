package infra

import (
	"fmt"

	"cost-manager/api/pkg/logger"
	"github.com/nats-io/nats.go"
)

func ConnectNats(natsURL string) (*nats.Conn, error) {
	const op = "infra.nats.connect"
	nc, err := nats.Connect(natsURL, nats.Name("Cost Manager API"))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to nats: %w", err)
	}

	logger.SysInfo(op, "Successfully connected to NATS broker")
	return nc, nil
}
