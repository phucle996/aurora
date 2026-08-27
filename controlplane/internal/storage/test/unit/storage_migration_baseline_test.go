package unit_test

import (
	"io/fs"
	"strings"
	"testing"

	storagemigrations "controlplane/internal/storage/migrations"
)

func TestStorageMigrationBaselineHasThreeDurableBoundaries(t *testing.T) {
	entries, err := fs.ReadDir(storagemigrations.Files, ".")
	if err != nil {
		t.Fatal(err)
	}
	upFiles := make([]string, 0, 3)
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".up.sql") {
			upFiles = append(upFiles, entry.Name())
		}
	}
	want := []string{
		"000001_storage_resources.up.sql",
		"000002_storage_job_outbox.up.sql",
		"000003_commercial_admission.up.sql",
	}
	if len(upFiles) != len(want) {
		t.Fatalf("expected three Storage baseline migrations, got %v", upFiles)
	}
	for index := range want {
		if upFiles[index] != want[index] {
			t.Fatalf("unexpected Storage baseline order: %v", upFiles)
		}
	}
}

func TestStorageMigrationBaselineContainsFinalSchemaOnly(t *testing.T) {
	resources, err := storagemigrations.Files.ReadFile("000001_storage_resources.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := storagemigrations.Files.ReadFile("000002_storage_job_outbox.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	admission, err := storagemigrations.Files.ReadFile("000003_commercial_admission.up.sql")
	if err != nil {
		t.Fatal(err)
	}

	resourceSQL := string(resources)
	for _, required := range []string{
		"personal_buckets",
		"tenant_buckets",
		"used_bytes_observed_at",
		"state IN ('CREATING', 'READY', 'DELETING', 'ERROR')",
		"trg_personal_credential_delete_requires_deleting",
		"trg_tenant_credential_delete_requires_deleting",
		"trg_personal_bucket_delete_requires_deleting",
		"trg_tenant_bucket_delete_requires_deleting",
	} {
		if !strings.Contains(resourceSQL, required) {
			t.Fatalf("resource baseline is missing %q", required)
		}
	}
	jobSQL := string(jobs)
	for _, required := range []string{"storage_outbox_records", "payload_key_id", "ownership_published_at"} {
		if !strings.Contains(jobSQL, required) {
			t.Fatalf("job baseline is missing %q", required)
		}
	}
	admissionSQL := string(admission)
	for _, required := range []string{
		"commercial_admission_projection",
		"resource_admission_projection",
		"commercial_admission_zone_outbox",
	} {
		if !strings.Contains(admissionSQL, required) {
			t.Fatalf("admission baseline is missing %q", required)
		}
	}
	all := resourceSQL + jobSQL + admissionSQL
	for _, obsolete := range []string{"resource_lifecycle_events", "access_session_auth_projection", "ALTER TABLE"} {
		if strings.Contains(all, obsolete) {
			t.Fatalf("greenfield baseline still contains obsolete migration behavior %q", obsolete)
		}
	}
}
