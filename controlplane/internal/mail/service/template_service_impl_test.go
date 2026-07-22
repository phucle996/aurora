package mailSvcImpl

import (
	"context"
	"crypto/sha256"
	"testing"

	mailEntity "controlplane/internal/mail/domain/entity"
	mailproto "controlplane/internal/mail/transport/rpc/proto"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

type personalTemplateRepoCapture struct {
	entity *mailEntity.PersonalTemplate
	outbox *mailEntity.MailOutboxRecord
}

func TestPersonalTemplateDeleteUsesNextRevisionAsTombstoneFence(t *testing.T) {
	workspaceID := uuid.New()
	repo := &personalTemplateRepoCapture{entity: &mailEntity.PersonalTemplate{
		ActorUserID: uuid.New(), ZoneID: uuid.New(), ID: uuid.NewString(), WorkspaceID: &workspaceID,
		CurrentVersion: 3, TemplateRevision: 4, NextVersion: 8, NextRevision: 9,
	}}
	command := &mailEntity.PersonalTemplate{
		ActorUserID: repo.entity.ActorUserID, ZoneID: repo.entity.ZoneID, WorkspaceID: &workspaceID,
		TemplateID: repo.entity.ID, ExpectedRevision: 4,
	}
	if err := NewPersonalTemplateService(repo).DeleteTemplate(context.Background(), command); err != nil {
		t.Fatalf("DeleteTemplate() error = %v", err)
	}
	var event mailproto.MailTemplateDeletedV1
	if err := proto.Unmarshal(repo.outbox.Payload, &event); err != nil {
		t.Fatalf("invalid delete payload: %v", err)
	}
	if event.TemplateRevision != 9 || command.OperationID != repo.outbox.EventID {
		t.Fatalf("delete did not use monotonic revision fence: event=%+v command=%+v", event, command)
	}
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
func (r *personalTemplateRepoCapture) Delete(_ context.Context, _ *mailEntity.PersonalTemplate, outbox *mailEntity.MailOutboxRecord) error {
	r.outbox = outbox
	return nil
}

func TestPersonalTemplateCreateUsesOneEntityAndOutbox(t *testing.T) {
	workspaceID := uuid.New()
	repo := &personalTemplateRepoCapture{}
	entity, err := NewPersonalTemplateService(repo).CreateTemplate(context.Background(), &mailEntity.PersonalTemplate{ActorUserID: uuid.New(), WorkspaceID: &workspaceID, ZoneID: uuid.New(), Code: "receipt", Name: "Receipt", SubjectTemplate: "Receipt {{id}}", HTMLTemplate: "<p>{{id}}</p>"})
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
	_, err := NewPersonalTemplateService(repo).CreateTemplate(context.Background(), &mailEntity.PersonalTemplate{ActorUserID: uuid.New(), WorkspaceID: &workspaceID, ZoneID: uuid.New(), Code: "runtime", Name: "Runtime", SubjectTemplate: "Hello {{name}}", HTMLTemplate: "<p>{{name}}</p>"})
	if err != nil || repo.entity == nil {
		t.Fatalf("template did not reach repository: %v", err)
	}
}

func TestPersonalTemplatePublishAllocatesCandidateWithoutAdvancingActiveHead(t *testing.T) {
	workspaceID := uuid.New()
	repo := &personalTemplateRepoCapture{entity: &mailEntity.PersonalTemplate{
		ActorUserID: uuid.New(), ZoneID: uuid.New(), ID: uuid.NewString(), WorkspaceID: &workspaceID,
		Code: "receipt", Name: "Receipt", CurrentVersion: 1, TemplateRevision: 1, NextVersion: 3, NextRevision: 4,
		TemplateID: "active", Version: 1, SubjectTemplate: "old", HTMLTemplate: "<p>old</p>",
	}}
	command := &mailEntity.PersonalTemplate{
		ActorUserID: repo.entity.ActorUserID, ZoneID: repo.entity.ZoneID, WorkspaceID: &workspaceID,
		TemplateID: repo.entity.ID, ExpectedRevision: 1, SubjectTemplate: "new", HTMLTemplate: "<p>new</p>",
	}
	candidate, err := NewPersonalTemplateService(repo).PublishTemplateVersion(context.Background(), command)
	if err != nil {
		t.Fatalf("PublishTemplateVersion() error = %v", err)
	}
	if candidate.CurrentVersion != 1 || candidate.Version != 3 || candidate.TemplateRevision != 4 || candidate.OperationID != repo.outbox.EventID {
		t.Fatalf("publish did not preserve active head and allocate candidate: %+v", candidate)
	}
}
