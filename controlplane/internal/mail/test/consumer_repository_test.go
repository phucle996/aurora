package mail_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"controlplane/internal/config"
	e "controlplane/internal/mail/domain/entity"
	"controlplane/internal/mail/migrations"
	r "controlplane/internal/mail/repository"
	taxonomy "controlplane/internal/mail/taxonomy"
	"controlplane/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This fixture isolates SQL concurrency from key distribution; production sealing
// is independently tested by the security module and is not bypassed in runtime.
type consumerTestProtector struct{}

func (consumerTestProtector) Seal(_ context.Context, _ security.Metadata, payload []byte) (*security.Protected, error) {
	return &security.Protected{Payload: payload, KeyID: uuid.New()}, nil
}

func TestConsumerDrainAndDeleteDurableFences(t *testing.T) {
	dsn := os.Getenv("AURORA_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("requires AURORA_TEST_POSTGRES")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := "mail_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = db.Exec(ctx, "CREATE SCHEMA "+schema)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") }()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, "SET LOCAL search_path TO "+schema+`;
CREATE TABLE personal_workspaces(id uuid PRIMARY KEY,zone_id uuid,owner_id uuid);
CREATE TABLE tenant_workspaces(id uuid PRIMARY KEY,zone_id uuid,tenant_id uuid);
CREATE TABLE tenant_memberships(tenant_id uuid,user_id uuid,status text);
CREATE TABLE personal_mail_consumers(id uuid PRIMARY KEY,workspace_id uuid,config_version bigint,parallelism int,desired_state text,updated_at timestamptz);
CREATE TABLE tenant_mail_consumers(LIKE personal_mail_consumers INCLUDING ALL);
CREATE TABLE mail_outbox_records(id bigserial PRIMARY KEY,event_id uuid UNIQUE,zone_id uuid,job_topic text,payload bytea,payload_key_id uuid,actor_user_id uuid,status text,job_version int,resource_id text,payload_schema_version int,idle int,trace_id bytea);
`)
	if err != nil {
		t.Fatal(err)
	}
	triggers, err := migrations.Files.ReadFile("000004_mail_triggers.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	consumerTriggers := strings.Split(string(triggers), "CREATE OR REPLACE FUNCTION reject_mail_template_version_mutation()")[0]
	if _, err = tx.Exec(ctx, consumerTriggers); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.SchemaSQL.Mail, cfg.SchemaSQL.Hierarchy = schema, schema
	personal := r.NewPersonalConsumerRepository(db, cfg, consumerTestProtector{})
	tenant := r.NewTenantConsumerRepository(db, cfg, consumerTestProtector{})
	for _, scope := range []string{"personal", "tenant"} {
		t.Run(scope, func(t *testing.T) {
			actor, zone, workspace, tenantID, id := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
			if scope == "personal" {
				_, err = db.Exec(ctx, fmt.Sprintf("INSERT INTO %s.personal_workspaces VALUES($1,$2,$3)", schema), workspace, zone, actor)
			} else {
				_, err = db.Exec(ctx, fmt.Sprintf("INSERT INTO %s.tenant_workspaces VALUES($1,$2,$3)", schema), workspace, zone, tenantID)
				if err == nil {
					_, err = db.Exec(ctx, fmt.Sprintf("INSERT INTO %s.tenant_memberships VALUES($1,$2,'active')", schema), tenantID, actor)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			table := schema + "." + scope + "_mail_consumers"
			if _, err = db.Exec(ctx, "INSERT INTO "+table+" VALUES($1,$2,7,4,'enabled',NOW())", id, workspace); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(ctx, "DELETE FROM "+table+" WHERE id=$1", id); err == nil {
				t.Fatal("DB allowed delete outside deleting")
			}
			personalCmd := e.PersonalConsumerDrainCommand{ActorUserID: actor, ZoneID: zone, WorkspaceID: workspace, ConsumerID: id, ExpectedConfigVersion: 7, TimeoutSeconds: 30}
			tenantCmd := e.TenantConsumerDrainCommand{ActorUserID: actor, ZoneID: zone, WorkspaceID: workspace, TenantID: tenantID, ConsumerID: id, ExpectedConfigVersion: 7, TimeoutSeconds: 30}
			if scope == "personal" {
				wrong := personalCmd
				wrong.ZoneID = uuid.New()
				_, err = personal.LoadDrainTarget(ctx, wrong)
			} else {
				wrong := tenantCmd
				wrong.TenantID = uuid.New()
				_, err = tenant.LoadDrainTarget(ctx, wrong)
			}
			if !errors.Is(err, taxonomy.ErrConsumerNotFound) {
				t.Fatalf("scope fence: %v", err)
			}
			var winners atomic.Int32
			var wg sync.WaitGroup
			for range 16 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					outbox := e.MailOutboxRecord{EventID: uuid.New(), ZoneID: zone, JobTopic: "mail.consumer.drain", ResourceID: id.String(), ActorUserID: actor, Idle: 60, Payload: []byte{1}}
					var requestErr error
					if scope == "personal" {
						requestErr = personal.RequestDrain(ctx, personalCmd, 4, outbox)
					} else {
						requestErr = tenant.RequestDrain(ctx, tenantCmd, 4, outbox)
					}
					if requestErr == nil {
						winners.Add(1)
					} else if !errors.Is(requestErr, taxonomy.ErrVersionConflict) {
						t.Errorf("drain SQL: %v", requestErr)
					}
				}()
			}
			wg.Wait()
			if winners.Load() != 1 {
				t.Fatalf("concurrent drain winners = %d", winners.Load())
			}
			outbox := e.MailOutboxRecord{EventID: uuid.New(), ZoneID: zone, JobTopic: "mail.consumer.delete", ResourceID: id.String(), ActorUserID: actor, Idle: 60, Payload: []byte{1}, Status: e.OutboxStatusPending, JobVersion: 1, PayloadSchemaVersion: 1}
			deletePersonal := e.DeletePersonalConsumer{ActorUserID: actor, ZoneID: zone, WorkspaceID: workspace, ID: id, ExpectedConfigVersion: 7}
			deleteTenant := e.DeleteTenantConsumer{ActorUserID: actor, ZoneID: zone, WorkspaceID: workspace, TenantID: tenantID, ID: id, ExpectedConfigVersion: 7}
			if scope == "personal" {
				err = personal.Delete(ctx, &deletePersonal, &outbox)
			} else {
				err = tenant.Delete(ctx, &deleteTenant, &outbox)
			}
			if !errors.Is(err, taxonomy.ErrOperationInProgress) {
				t.Fatalf("delete bypassed live drain: %v", err)
			}
			if _, err = db.Exec(ctx, "UPDATE "+schema+".mail_outbox_records SET status='SUCCEEDED' WHERE resource_id=$1", id.String()); err != nil {
				t.Fatal(err)
			}
			if scope == "personal" {
				err = personal.Delete(ctx, &deletePersonal, &outbox)
			} else {
				err = tenant.Delete(ctx, &deleteTenant, &outbox)
			}
			if !errors.Is(err, taxonomy.ErrVersionConflict) {
				t.Fatalf("delete bypassed drained state: %v", err)
			}
			// Simulate JO's separately tested success transaction; delete request must keep the row.
			if _, err = db.Exec(ctx, "UPDATE "+table+" SET desired_state='drained' WHERE id=$1", id); err != nil {
				t.Fatal(err)
			}
			if scope == "personal" {
				err = personal.Delete(ctx, &deletePersonal, &outbox)
			} else {
				err = tenant.Delete(ctx, &deleteTenant, &outbox)
			}
			if err != nil {
				t.Fatalf("delete drained consumer: %v", err)
			}
			var state string
			if err = db.QueryRow(ctx, "SELECT desired_state FROM "+table+" WHERE id=$1", id).Scan(&state); err != nil || state != "deleting" {
				t.Fatalf("request deleted row or wrong state: %s %v", state, err)
			}
			if _, err = db.Exec(ctx, "DELETE FROM "+table+" WHERE id=$1", id); err != nil {
				t.Fatalf("confirmed delete blocked: %v", err)
			}
		})
	}
}
