package bootstrap

import (
	"controlplane/internal/config"
	"controlplane/pkg/logger"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type GRPCServer struct {
	name   string
	Server *grpc.Server
	lis    net.Listener
}

type GRPC struct {
	Server *GRPCServer
}

func InitGRPCServer(cfg *config.GRPCCfg) (*GRPC, error) {
	if cfg == nil {
		return nil, nil
	}

	port := strings.TrimSpace(cfg.Port)
	if port == "" {
		return nil, errors.New("grpc: port is required")
	}
	if strings.TrimSpace(cfg.TLSCertPath) == "" || strings.TrimSpace(cfg.TLSKeyPath) == "" {
		return nil, errors.New("grpc: tls cert and key are required")
	}
	if strings.TrimSpace(cfg.ClientCACertPath) == "" {
		return nil, errors.New("grpc: client ca is required")
	}

	serverCert, err := tls.LoadX509KeyPair(cfg.TLSCertPath, cfg.TLSKeyPath)
	if err != nil {
		return nil, fmt.Errorf("grpc: load server cert/key: %w", err)
	}

	clientCAPEM, err := os.ReadFile(cfg.ClientCACertPath)
	if err != nil {
		return nil, fmt.Errorf("grpc: read client ca: %w", err)
	}
	clientCAPool := x509.NewCertPool()
	if ok := clientCAPool.AppendCertsFromPEM(clientCAPEM); !ok {
		return nil, errors.New("grpc: append client ca")
	}

	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return nil, fmt.Errorf("grpc: listen port %s: %w", port, err)
	}

	logger.SysInfo("grpc", "controlplane gRPC server configured with mTLS")
	serverTLSConfig := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    clientCAPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("grpc: missing peer certificate")
			}

			peerCert := cs.PeerCertificates[0]
			logger.SysInfo("grpc", fmt.Sprintf("accepted runtime client certificate subject=%s issuer=%s", peerCert.Subject.String(), peerCert.Issuer.String()))
			return nil
		},
	}

	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLSConfig)))
	return &GRPC{Server: &GRPCServer{name: "controlplane", Server: server, lis: lis}}, nil
}

func InitGRPCClient(target string, tlsConfig *tls.Config) (*grpc.ClientConn, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, errors.New("grpc: client target is required")
	}
	if tlsConfig == nil {
		return nil, errors.New("grpc: client tls config is required")
	}

	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	if err != nil {
		return nil, fmt.Errorf("grpc: init client %s: %w", target, err)
	}
	return conn, nil
}

func (g *GRPC) Start() error {
	if g == nil || g.Server == nil {
		return nil
	}
	return g.Server.Start()
}

func (s *GRPCServer) Start() error {
	if s == nil || s.Server == nil || s.lis == nil {
		return nil
	}
	return s.Server.Serve(s.lis)
}

func (g *GRPC) Stop() {
	if g == nil || g.Server == nil {
		return
	}
	g.Server.Stop()
}

func (s *GRPCServer) Stop() {
	if s == nil {
		return
	}
	if s.lis != nil {
		_ = s.lis.Close()
	}
	done := make(chan struct{})
	go func() {
		s.Server.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		logger.SysWarn("grpc", fmt.Sprintf("%s gRPC graceful stop timed out; forcing stop", s.name))
		s.Server.Stop()
		<-done
	}
}
