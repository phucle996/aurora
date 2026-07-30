package bootstrap

import (
	"context"
	"controlplane/internal/config"
	"controlplane/internal/observability"
	"controlplane/pkg/logger"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

type GRPCServer struct {
	name   string
	Server *grpc.Server
	lis    net.Listener
}

type GRPC struct {
	Server *GRPCServer
}

func InitGRPCServer(cfg *config.GRPCCfg, obs *observability.OTel) (*GRPC, error) {
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

	// [COMMENT]: Khởi tạo các server options cho gRPC. Cấu hình TLS làm cơ sở.
	opts := []grpc.ServerOption{
		grpc.Creds(credentials.NewTLS(serverTLSConfig)),
	}

	// [COMMENT]: Nếu OpenTelemetry được kích hoạt, đăng ký interceptor để nhận trace parent.
	if obs != nil {
		opts = append(opts,
			grpc.UnaryInterceptor(UnaryServerInterceptor(obs)),
			grpc.StreamInterceptor(StreamServerInterceptor(obs)),
		)
	}

	server := grpc.NewServer(opts...)
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

// ============================================================================
// 🔄 THÀNH PHẦN ADAPTER VÀ INTERCEPTOR GHI VẾT CHO GRPC SERVER
// ============================================================================

// metadataCarrier chuyển đổi kiểu gRPC metadata sang W3C propagation.TextMapCarrier
type metadataCarrier metadata.MD

// Get trích xuất giá trị đầu tiên theo key từ metadata
func (mc metadataCarrier) Get(key string) string {
	values := metadata.MD(mc).Get(key)
	if len(values) > 0 {
		return values[0]
	}
	return ""
}

// Set chèn giá trị mới theo key vào metadata
func (mc metadataCarrier) Set(key string, value string) {
	metadata.MD(mc).Set(key, value)
}

// Keys trả về toàn bộ danh sách key trong metadata
func (mc metadataCarrier) Keys() []string {
	keys := make([]string, 0, len(metadata.MD(mc)))
	for k := range metadata.MD(mc) {
		keys = append(keys, k)
	}
	return keys
}

// UnaryServerInterceptor khởi tạo bộ lọc unary giúp giải nén và lưu vết traceparent từ client
func UnaryServerInterceptor(obs *observability.OTel) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		if obs == nil {
			return handler(ctx, req)
		}

		// [COMMENT]: Lấy metadata từ context hiện tại
		md, ok := metadata.FromIncomingContext(ctx)
		if ok {
			// [COMMENT]: Giải nén định danh trace context từ metadata
			ctx = otel.GetTextMapPropagator().Extract(ctx, metadataCarrier(md))
		}

		// [COMMENT]: Bắt đầu một server span con mới đại diện cho RPC call
		ctx, span := obs.StartServerSpan(ctx, "gRPC "+info.FullMethod)
		defer span.End()

		span.SetAttributes(
			attribute.String("rpc.system", "grpc"),
			attribute.String("rpc.method", info.FullMethod),
		)

		resp, err := handler(ctx, req)
		if err != nil {
			span.RecordError(err)
		}
		return resp, err
	}
}

// StreamServerInterceptor khởi tạo bộ lọc stream giúp giải nén và lưu vết traceparent từ client
func StreamServerInterceptor(obs *observability.OTel) grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if obs == nil {
			return handler(srv, ss)
		}

		ctx := ss.Context()
		md, ok := metadata.FromIncomingContext(ctx)
		if ok {
			// [COMMENT]: Giải nén định danh trace context từ metadata
			ctx = otel.GetTextMapPropagator().Extract(ctx, metadataCarrier(md))
		}

		// [COMMENT]: Bắt đầu một server span con mới đại diện cho stream gRPC call
		ctx, span := obs.StartServerSpan(ctx, "gRPC "+info.FullMethod)
		defer span.End()

		span.SetAttributes(
			attribute.String("rpc.system", "grpc"),
			attribute.String("rpc.method", info.FullMethod),
		)

		// [COMMENT]: Bọc ServerStream để cung cấp context mới chứa trace
		wrapped := &wrappedStream{ServerStream: ss, ctx: ctx}
		err := handler(srv, wrapped)
		if err != nil {
			span.RecordError(err)
		}
		return err
	}
}

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

// Context trả về context chứa thông tin tracing span hiện tại
func (w *wrappedStream) Context() context.Context {
	return w.ctx
}
