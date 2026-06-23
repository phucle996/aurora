package repo_test

import (
	"context"
	"testing"
	"time"

	iamRepoImpl "controlplane/internal/iam/repository"
	"controlplane/internal/iam/test/testutil"

	"github.com/google/uuid"
)

func TestRefreshTokenRepoRevokeByScope(t *testing.T) {
	cfg := testutil.NewIAMTestConfig(testutil.UniqueSchema("iam_refresh_repo_scope"))
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareIAMSchema(t, cfg, db)
	repo := iamRepoImpl.NewRefreshTokenRepository(cfg, db)
	ctx := context.Background()

	username, email := testutil.UniqueIdentity("refresh_scope")
	userID := uuid.New()
	now := time.Now().UTC()
	_, err := db.Exec(ctx,
		"INSERT INTO "+cfg.SchemaSQL.IAM+".users (id,username,email,phone,password_hash,status,created_at,updated_at) VALUES ($1,$2,$3,NULL,$4,'active',$5,$6)",
		userID, username, email, "hash", now, now,
	)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	d1 := uuid.New()
	d2 := uuid.New()
	_, err = db.Exec(ctx,
		"INSERT INTO "+cfg.SchemaSQL.IAM+".devices (id,user_id,device_name,public_key,public_key_alg,public_key_fingerprint,status,created_at,updated_at) VALUES ($1,$2,'D1','pk1','Ed25519',$3,'recognized',$4,$5),($6,$2,'D2','pk2','Ed25519',$7,'recognized',$4,$5)",
		d1, userID, "fp-"+d1.String(), now, now, d2, "fp-"+d2.String(),
	)
	if err != nil {
		t.Fatalf("seed devices: %v", err)
	}

	_, err = db.Exec(ctx,
		"INSERT INTO "+cfg.SchemaSQL.IAM+".refresh_tokens (id,user_id,device_id,token_hash,tenant_id,issued_at,expires_at) VALUES ($1,$2,$3,$4,NULL,$5,$6),($7,$2,$8,$9,NULL,$5,$6)",
		uuid.New(), userID, d1, "h1", now, now.Add(24*time.Hour),
		uuid.New(), d2, "h2",
	)
	if err != nil {
		t.Fatalf("seed refresh tokens: %v", err)
	}

	affected, err := repo.RevokeRefreshTokensByUserID(ctx, userID, &d1)
	if err != nil {
		t.Fatalf("revoke scoped: %v", err)
	}
	if affected != 1 {
		t.Fatalf("expected affected=1, got %d", affected)
	}
}
