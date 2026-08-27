package integration_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"controlplane/internal/config"
	entity "controlplane/internal/hypervisor/domain/entity"
	"controlplane/internal/hypervisor/migrations"
	repository "controlplane/internal/hypervisor/repository"
	taxonomy "controlplane/internal/hypervisor/taxonomy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestResourcePlanProjectionConverges(t *testing.T) {
	dsn := os.Getenv("AURORA_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("AURORA_TEST_POSTGRES is required for real PostgreSQL integration")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := "plan_projection_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := db.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") }()
	migration, err := migrations.Files.ReadFile("000002_hypervisor_tables.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(migration), "CREATE TABLE IF NOT EXISTS hypervisor_resource_plan_revisions (")
	end := strings.Index(string(migration)[start:], "\n);") + start + 3
	table := strings.Replace(string(migration)[start:end], "hypervisor_resource_plan_revisions", schema+".hypervisor_resource_plan_revisions", 1)
	if _, err := db.Exec(ctx, table); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.SchemaSQL.Hypervisor = schema
	repo := repository.NewHypervisorResourcePlanProjectionRepository(db, cfg)
	base := time.Now().UTC().Truncate(time.Second).Add(-3 * time.Hour)
	for _, order := range [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}, {-1}} {
		t.Run(fmt.Sprint(order), func(t *testing.T) {
			planID := uuid.New()
			revisions := make([]entity.HypervisorResourcePlanProjection, 3)
			for i := range revisions {
				revisions[i] = entity.HypervisorResourcePlanProjection{
					PlanID: planID, RevisionID: uuid.New(), RevisionNumber: int64(i + 1),
					Code: "test-plan", DisplayName: "Test", BillingModel: "LIMIT_HOURLY",
					CPUCores: 2, MemoryMIB: 4096, BootDiskGIB: 64, ContentSHA256: make([]byte, 32),
					EffectiveFrom: base.Add(time.Duration(i) * time.Hour), State: "ACTIVE", AllowCreate: true, SourceEventID: uuid.New(),
				}
			}
			if order[0] < 0 {
				var wg sync.WaitGroup
				results := make(chan error, 12)
				for i := 0; i < 12; i++ {
					wg.Add(1)
					go func(index int) { defer wg.Done(); results <- repo.Insert(ctx, &revisions[index%3]) }(i)
				}
				wg.Wait()
				close(results)
				for err := range results {
					if err != nil {
						t.Fatal(err)
					}
				}
			} else {
				for _, index := range order {
					if err := repo.Insert(ctx, &revisions[index]); err != nil {
						t.Fatal(err)
					}
				}
			}
			// Replaying a predecessor must not reopen or extend a closed window.
			if err := repo.Insert(ctx, &revisions[0]); err != nil {
				t.Fatal(err)
			}
			for i, revision := range revisions {
				var end *time.Time
				if err := db.QueryRow(ctx, "SELECT effective_to FROM "+schema+".hypervisor_resource_plan_revisions WHERE revision_id=$1", revision.RevisionID).Scan(&end); err != nil {
					t.Fatal(err)
				}
				if i < 2 && (end == nil || !end.Equal(revisions[i+1].EffectiveFrom)) {
					t.Fatalf("r%d wrong successor boundary: %v", i+1, end)
				}
				if i == 2 && end != nil {
					t.Fatalf("last revision unexpectedly closed: %v", end)
				}
			}
			var active int
			if err := db.QueryRow(ctx, "SELECT count(*) FROM "+schema+".hypervisor_resource_plan_revisions WHERE plan_id=$1 AND effective_from<=NOW() AND (effective_to IS NULL OR NOW()<effective_to)", planID).Scan(&active); err != nil {
				t.Fatal(err)
			}
			if active != 1 {
				t.Fatalf("expected one admissible revision, got %d", active)
			}
			conflict := revisions[0]
			conflict.CPUCores = 8
			if err := repo.Insert(ctx, &conflict); !errors.Is(err, taxonomy.ErrInvalidResourcePlanProjection) {
				t.Fatalf("immutable conflict accepted: %v", err)
			}
			conflict = revisions[0]
			conflict.RevisionID = uuid.New()
			if err := repo.Insert(ctx, &conflict); !errors.Is(err, taxonomy.ErrInvalidResourcePlanProjection) {
				t.Fatalf("revision-number collision accepted: %v", err)
			}
			conflict = revisions[2]
			conflict.RevisionID = uuid.New()
			conflict.RevisionNumber = 4
			conflict.EffectiveFrom = base
			if err := repo.Insert(ctx, &conflict); !errors.Is(err, taxonomy.ErrInvalidResourcePlanProjection) {
				t.Fatalf("nonmonotonic time accepted: %v", err)
			}
		})
	}
}
