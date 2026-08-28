package unit

import (
	"context"
	"testing"
	"time"

	"controlplane/internal/cacheengine"
	iamService "controlplane/internal/iam/service"
	"controlplane/internal/iam/transport/proto"
	iamPubsubHandler "controlplane/internal/iam/transport/pubsub/handler"
	"controlplane/internal/observability"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

func TestPersonalRuntimeReadAuthorizationUsesOnlyPlatformRole(t *testing.T) {
	server := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	registry := cacheengine.NewCacheRegistry(cacheengine.NewL1Cache(), observability.NewNoopCacheRecorder())
	actorID := uuid.New()
	workspaceID := uuid.New()
	cacheengine.Register(registry, "user_role", time.Minute, func(_ context.Context, parameter string) (*iamproto.RoleEntry, error) {
		if parameter != actorID.String() {
			t.Fatalf("unexpected personal role key: %s", parameter)
		}
		return &iamproto.RoleEntry{Permissions: []string{
			"alice:" + workspaceID.String() + ":email:consumer:read",
		}}, nil
	})

	personal := iamService.NewPersonalRuntimeReadAuthorizationService(registry)
	tenantRegistry := cacheengine.NewCacheRegistry(cacheengine.NewL1Cache(), observability.NewNoopCacheRecorder())
	cacheengine.Register(tenantRegistry, "membership_role", time.Minute, func(_ context.Context, _ string) (*iamproto.RoleEntry, error) {
		t.Fatal("Personal authorization must not load Tenant membership authority")
		return nil, nil
	})
	tenant := iamService.NewTenantRuntimeReadAuthorizationService(tenantRegistry)
	handler, err := iamPubsubHandler.NewRuntimeReadAuthorizationRedisHandler(client, personal, tenant)
	if err != nil {
		t.Fatalf("create personal runtime-read handler: %v", err)
	}
	if err := handler.Start(); err != nil {
		t.Fatalf("start personal runtime-read handler: %v", err)
	}
	t.Cleanup(handler.Stop)

	requestID := uuid.New()
	reply := client.Subscribe(context.Background(), "iam.authorization.runtime.reply."+requestID.String())
	if _, err := reply.Receive(context.Background()); err != nil {
		t.Fatalf("subscribe personal runtime-read reply: %v", err)
	}
	t.Cleanup(func() { _ = reply.Close() })
	request, err := proto.Marshal(&iamproto.RuntimeReadAuthorizationRequestV1{
		ActorUserId:   actorID[:],
		ActorUsername: "alice",
		WorkspaceId:   workspaceID[:],
		Permission:    "email:consumer:read",
	})
	if err != nil {
		t.Fatalf("marshal personal runtime-read request: %v", err)
	}
	payload := append(append([]byte(nil), requestID[:]...), request...)
	if err := client.Publish(context.Background(), "iam.authorization.runtime.get", payload).Err(); err != nil {
		t.Fatalf("publish personal runtime-read request: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	message, err := reply.ReceiveMessage(ctx)
	if err != nil {
		t.Fatalf("receive personal runtime-read reply: %v", err)
	}
	var response iamproto.RuntimeReadAuthorizationResponseV1
	if err := proto.Unmarshal([]byte(message.Payload), &response); err != nil {
		t.Fatalf("decode personal runtime-read reply: %v", err)
	}
	if !response.Allowed {
		t.Fatal("expected exact Personal permission to be allowed")
	}
}
