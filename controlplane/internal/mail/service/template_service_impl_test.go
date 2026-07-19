package mailSvcImpl

import (
	"context"
	"crypto/sha256"
	"testing"

	mailEntity "controlplane/internal/mail/domain/entity"

	"github.com/google/uuid"
)

type personalTemplateRepoCapture struct {
	entity *mailEntity.PersonalTemplate
	outbox *mailEntity.MailOutboxRecord
}

func (r *personalTemplateRepoCapture) Create(_ context.Context, entity *mailEntity.PersonalTemplate, outbox *mailEntity.MailOutboxRecord) error {
	r.entity, r.outbox = entity, outbox
	return nil
}
func (r *personalTemplateRepoCapture) GetByID(_ context.Context, _ *mailEntity.PersonalTemplate) (*mailEntity.PersonalTemplate, error) {
	return r.entity, nil
}
func (r *personalTemplateRepoCapture) List(_ context.Context, _ *mailEntity.PersonalTemplate) ([]*mailEntity.PersonalTemplate, error) {
	return nil, nil
}
func (r *personalTemplateRepoCapture) ListVersions(_ context.Context, _ *mailEntity.PersonalTemplate) ([]*mailEntity.PersonalTemplate, error) {
	return nil, nil
}
func (r *personalTemplateRepoCapture) PublishVersion(_ context.Context, entity *mailEntity.PersonalTemplate, outbox *mailEntity.MailOutboxRecord) error {
	r.entity, r.outbox = entity, outbox
	return nil
}
func (r *personalTemplateRepoCapture) Archive(_ context.Context, _ *mailEntity.PersonalTemplate, outbox *mailEntity.MailOutboxRecord) error {
	r.outbox = outbox
	return nil
}

func TestPersonalTemplateCreateUsesOneEntityAndOutbox(t *testing.T) {
	workspaceID := uuid.New()
	repo := &personalTemplateRepoCapture{}
	entity, err := NewPersonalTemplateService(repo).CreateTemplate(context.Background(), &mailEntity.PersonalTemplate{ActorUserID: uuid.New(), WorkspaceID: &workspaceID, ZoneID: uuid.New(), IdempotencyKey: "request-0001", Name: "Receipt", SubjectTemplate: "Receipt {{id}}", HTMLTemplate: "<p>{{id}}</p>"})
	if err != nil {
		t.Fatalf("CreateTemplate() error = %v", err)
	}
	if entity.Version != 1 || entity.CurrentVersion != 1 || len(entity.ContentSHA256) != sha256.Size {
		t.Fatalf("unexpected entity: %+v", entity)
	}
	if repo.outbox == nil || repo.outbox.JobTopic != "mail.template.version_published" {
		t.Fatalf("unexpected outbox: %+v", repo.outbox)
	}
}

func TestPersonalTemplateLeavesPlaceholderDetectionToDataplane(t *testing.T) {
	workspaceID := uuid.New()
	repo := &personalTemplateRepoCapture{}
	_, err := NewPersonalTemplateService(repo).CreateTemplate(context.Background(), &mailEntity.PersonalTemplate{ActorUserID: uuid.New(), WorkspaceID: &workspaceID, ZoneID: uuid.New(), IdempotencyKey: "request-0002", Name: "Runtime", SubjectTemplate: "Hello {{name}}", HTMLTemplate: "<p>{{name}}</p>"})
	if err != nil || repo.entity == nil {
		t.Fatalf("template did not reach repository: %v", err)
	}
}
