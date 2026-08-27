package integration_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"cost-manager/api/internal/domain/entity"
	repoPort "cost-manager/api/internal/domain/repo"
	"cost-manager/api/internal/repository"
	"cost-manager/api/internal/service"
	taxonomy "cost-manager/api/internal/taxonomy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func TestResourcePlanAdminReadsAndLeaseFences(t *testing.T) {
	dsn := os.Getenv("AURORA_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("AURORA_TEST_POSTGRES required")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := "aurora_plan_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(ctx, "DROP DATABASE "+name) }()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, source, _, _ := runtime.Caller(0)
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(source), "../../..", "migrations/000003_tables_pricing.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(migration)
	start, end := strings.Index(text, "CREATE TABLE billing.hypervisor_resource_plans ("), strings.Index(text, "-- Mail owns its Zone multiplier.")
	if _, err = db.Exec(ctx, "CREATE EXTENSION btree_gist; CREATE SCHEMA billing; "+text[start:end]); err != nil {
		t.Fatal(err)
	}
	repo := repository.NewHypervisorResourcePlanRepository(db)
	svc := service.NewHypervisorResourcePlanService(repo, nil, entity.HypervisorResourcePlanRelayPolicy{})
	now := time.Now().UTC().Truncate(time.Microsecond)
	plans := make([]entity.HypervisorResourcePlanRevision, 0, 3)
	for i, code := range []string{"current", "future", "another"} {
		at := now.Add(-time.Hour)
		if i == 1 {
			at = now.Add(24 * time.Hour)
		}
		plan, err := svc.CreateHypervisorResourcePlan(ctx, entity.CreateHypervisorResourcePlanCommand{Code: code, DisplayName: code, CPUCores: 2, MemoryMIB: 4096, BootDiskGIB: 64, EffectiveFrom: at, ChangeReason: "initial", CreatedBy: uuid.New()})
		if err != nil {
			t.Fatal(err)
		}
		plans = append(plans, *plan)
	}
	_, err = svc.PublishHypervisorResourcePlanRevision(ctx, entity.PublishHypervisorResourcePlanRevisionCommand{PlanID: plans[0].PlanID, ExpectedLatestRevision: 1, CPUCores: 4, MemoryMIB: 8192, BootDiskGIB: 128, EffectiveFrom: now.Add(time.Hour), ChangeReason: "scheduled", CreatedBy: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	list, more, err := repo.ListPlans(ctx, entity.HypervisorResourcePlanAdminQuery{Limit: 2, At: now})
	if err != nil || !more || len(list) != 2 {
		t.Fatalf("page1=%#v more=%v err=%v", list, more, err)
	}
	tail, more, err := repo.ListPlans(ctx, entity.HypervisorResourcePlanAdminQuery{After: list[1].PlanID, Limit: 2, At: now})
	if err != nil || more || len(tail) != 1 {
		t.Fatalf("page2=%#v more=%v err=%v", tail, more, err)
	}
	for _, plan := range append(list, tail...) {
		if plan.PlanID == plans[0].PlanID && (plan.LatestRevisionNumber != 2 || plan.EffectiveRevisionNumber != 1) {
			t.Fatalf("latest/effective conflated: %#v", plan)
		}
		if plan.PlanID == plans[1].PlanID && (plan.LatestRevisionNumber != 1 || plan.EffectiveRevisionNumber != 0) {
			t.Fatalf("scheduled plan missing: %#v", plan)
		}
	}
	history, more, err := repo.ListRevisions(ctx, entity.HypervisorResourcePlanHistoryQuery{PlanID: plans[0].PlanID, Limit: 1, At: now})
	if err != nil || !more || len(history) != 1 || !history[0].IsLatest || history[0].IsEffective || history[0].RevisionNumber != 2 {
		t.Fatalf("history=%#v more=%v err=%v", history, more, err)
	}
	history, more, err = repo.ListRevisions(ctx, entity.HypervisorResourcePlanHistoryQuery{PlanID: plans[0].PlanID, Before: 2, Limit: 1, At: now})
	if err != nil || more || len(history) != 1 || history[0].IsLatest || !history[0].IsEffective {
		t.Fatalf("older=%#v more=%v err=%v", history, more, err)
	}
	_, err = svc.PublishHypervisorResourcePlanRevision(ctx, entity.PublishHypervisorResourcePlanRevisionCommand{PlanID: plans[0].PlanID, ExpectedLatestRevision: 1, CPUCores: 4, MemoryMIB: 8192, BootDiskGIB: 128, EffectiveFrom: now.Add(2 * time.Hour), ChangeReason: "stale", CreatedBy: uuid.New()})
	if !errors.Is(err, taxonomy.ErrHypervisorResourcePlanConflict) {
		t.Fatalf("stale OCC accepted: %v", err)
	}
	_, err = svc.PublishHypervisorResourcePlanRevision(ctx, entity.PublishHypervisorResourcePlanRevisionCommand{PlanID: plans[0].PlanID, ExpectedLatestRevision: 2, CPUCores: 4, MemoryMIB: 8192, BootDiskGIB: 128, EffectiveFrom: now.Add(2 * time.Hour), ChangeReason: "next", CreatedBy: uuid.New()})
	if err != nil {
		t.Fatalf("scheduled latest could not publish: %v", err)
	}
	// Other pods cannot claim live leases, and expired/wrong tokens cannot publish.
	token := uuid.New()
	rows, err := repo.ClaimHypervisorResourcePlanOutbox(ctx, token, now.Add(time.Minute), 100)
	if err != nil || len(rows) != 5 {
		t.Fatalf("claim=%d err=%v", len(rows), err)
	}
	other, err := repo.ClaimHypervisorResourcePlanOutbox(ctx, uuid.New(), now.Add(time.Minute), 100)
	if err != nil || len(other) != 0 {
		t.Fatalf("live lease stolen: %v %v", other, err)
	}
	if err := repo.MarkHypervisorResourcePlanOutboxPublished(ctx, rows[0].ID, uuid.New()); err == nil {
		t.Fatal("wrong token accepted")
	}
	if _, err := db.Exec(ctx, "UPDATE billing.hypervisor_resource_plan_outbox SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1", rows[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkHypervisorResourcePlanOutboxPublished(ctx, rows[0].ID, token); err == nil {
		t.Fatal("expired lease accepted")
	}
	newToken := uuid.New()
	reclaimed, err := repo.ClaimHypervisorResourcePlanOutbox(ctx, newToken, time.Now().Add(time.Minute), 1)
	if err != nil || len(reclaimed) != 1 {
		t.Fatalf("reclaim=%v err=%v", reclaimed, err)
	}
	if err := repo.MarkHypervisorResourcePlanOutboxPublished(ctx, reclaimed[0].ID, newToken); err != nil {
		t.Fatal(err)
	}
}

type resourcePlanRelayRepository struct {
	repoPort.HypervisorResourcePlanRepository
	row     entity.HypervisorResourcePlanOutboxRow
	claimed bool
	outcome chan bool
}

func (r *resourcePlanRelayRepository) ClaimHypervisorResourcePlanOutbox(_ context.Context, token uuid.UUID, _ time.Time, _ int) ([]entity.HypervisorResourcePlanOutboxRow, error) {
	if r.claimed {
		return nil, nil
	}
	r.claimed = true
	r.row.ClaimToken = token
	return []entity.HypervisorResourcePlanOutboxRow{r.row}, nil
}
func (r *resourcePlanRelayRepository) MarkHypervisorResourcePlanOutboxPublished(_ context.Context, id, token uuid.UUID) error {
	if id != r.row.ID || token != r.row.ClaimToken {
		return errors.New("claim fence mismatch")
	}
	r.outcome <- true
	return nil
}
func (r *resourcePlanRelayRepository) RetryHypervisorResourcePlanOutbox(_ context.Context, id, token uuid.UUID, _ string, _ time.Time) error {
	if id != r.row.ID || token != r.row.ClaimToken {
		return errors.New("claim fence mismatch")
	}
	r.outcome <- false
	return nil
}
func TestResourcePlanRelayRequiresDurability(t *testing.T) {
	addr := os.Getenv("AURORA_TEST_REDIS")
	if addr == "" {
		t.Skip("AURORA_TEST_REDIS required (standalone Redis with AOF, no replicas)")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	for _, replicas := range []int{1, 0} {
		repo := &resourcePlanRelayRepository{row: entity.HypervisorResourcePlanOutboxRow{ID: uuid.New(), EventID: uuid.New(), Payload: []byte("test")}, outcome: make(chan bool, 1)}
		ctx, cancel := context.WithCancel(context.Background())
		svc := service.NewHypervisorResourcePlanService(repo, client, entity.HypervisorResourcePlanRelayPolicy{ReplicaAcks: replicas, DurableWait: 2 * time.Second})
		done := make(chan struct{})
		go func() { defer close(done); svc.RunHypervisorResourcePlanOutboxRelay(ctx) }()
		select {
		case published := <-repo.outcome:
			if published != (replicas == 0) {
				t.Errorf("replicas=%d published=%v", replicas, published)
			}
		case <-time.After(5 * time.Second):
			t.Error("relay did not settle")
		}
		cancel()
		<-done
	}
}
func TestResourcePlanRelayUsesClusterPrimary(t *testing.T) {
	addr := os.Getenv("AURORA_TEST_REDIS_CLUSTER")
	if addr == "" {
		t.Skip("AURORA_TEST_REDIS_CLUSTER required (AOF primary+replica cluster)")
	}
	client := redis.NewClusterClient(&redis.ClusterOptions{Addrs: []string{addr}})
	defer client.Close()
	repo := &resourcePlanRelayRepository{row: entity.HypervisorResourcePlanOutboxRow{ID: uuid.New(), EventID: uuid.New(), Payload: []byte("cluster")}, outcome: make(chan bool, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := service.NewHypervisorResourcePlanService(repo, client, entity.HypervisorResourcePlanRelayPolicy{ReplicaAcks: 1, DurableWait: 2 * time.Second})
	done := make(chan struct{})
	go func() { defer close(done); svc.RunHypervisorResourcePlanOutboxRelay(ctx) }()
	select {
	case published := <-repo.outcome:
		if !published {
			t.Error("cluster primary durability failed")
		}
	case <-time.After(8 * time.Second):
		t.Error("cluster relay timed out")
	}
	cancel()
	<-done
}

// Opt-in: promotes a replica in a disposable test cluster, never a shared deployment.
func TestResourcePlanRelayRefreshesAfterFailover(t *testing.T) {
	addr := os.Getenv("AURORA_TEST_REDIS_CLUSTER")
	if addr == "" || os.Getenv("AURORA_TEST_ALLOW_CLUSTER_FAILOVER") != "1" {
		t.Skip("requires a disposable AURORA_TEST_REDIS_CLUSTER and AURORA_TEST_ALLOW_CLUSTER_FAILOVER=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := redis.NewClusterClient(&redis.ClusterOptions{Addrs: []string{addr}})
	defer client.Close()
	const stream = "billing.hypervisor.resource-plan.changed.v1"
	primary, err := client.MasterForKey(ctx, stream) // Cache the old topology before promotion.
	if err != nil {
		t.Fatal(err)
	}
	slots, err := client.ClusterSlots(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	var replicaAddr string
	for _, slot := range slots {
		if len(slot.Nodes) > 1 && slot.Nodes[0].Addr == primary.Options().Addr {
			replicaAddr = slot.Nodes[1].Addr
			break
		}
	}
	if replicaAddr == "" {
		t.Fatal("stream primary has no replica")
	}
	replica := redis.NewClient(&redis.Options{Addr: replicaAddr})
	defer replica.Close()
	if err := replica.Do(ctx, "CLUSTER", "FAILOVER").Err(); err != nil {
		t.Fatal(err)
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		role, err := replica.Do(ctx, "ROLE").Slice()
		if err == nil && len(role) > 0 && role[0] == "master" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("replica promotion timed out")
		case <-tick.C:
		}
	}
	// Keep the same event identity across retries and the same cached ClusterClient.
	row := entity.HypervisorResourcePlanOutboxRow{ID: uuid.New(), EventID: uuid.New(), Payload: []byte("failover")}
	for ctx.Err() == nil {
		repo := &resourcePlanRelayRepository{row: row, outcome: make(chan bool, 1)}
		runCtx, stop := context.WithCancel(ctx)
		svc := service.NewHypervisorResourcePlanService(repo, client, entity.HypervisorResourcePlanRelayPolicy{ReplicaAcks: 1, DurableWait: 2 * time.Second})
		done := make(chan struct{})
		go func() { defer close(done); svc.RunHypervisorResourcePlanOutboxRelay(runCtx) }()
		published := false
		select {
		case published = <-repo.outcome:
		case <-ctx.Done():
		}
		stop()
		<-done
		if published {
			entries, err := replica.XRevRangeN(ctx, stream, "+", "-", 1).Result()
			if err != nil || len(entries) != 1 || entries[0].Values["event_id"] != row.EventID.String() {
				t.Fatalf("event was not durably published on the new primary: %v %v", entries, err)
			}
			return
		}
		select {
		case <-ctx.Done():
		case <-tick.C:
		}
	}
	t.Fatal("relay did not recover from the stale cluster topology")
}
