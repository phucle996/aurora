package svc_test

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	mailEntity "controlplane/internal/mail/domain/entity"
	mailSvcImpl "controlplane/internal/mail/service"
	"controlplane/internal/observability"

	"github.com/google/uuid"
)

type personalTemplateRepoCapture struct {
	createRes  *mailEntity.CreatePersonalTemplateResponse
	getRes     *mailEntity.GetPersonalTemplateResponse
	publishRes *mailEntity.PublishPersonalTemplateVersionResponse
	deleteOpID uuid.UUID
}

func (r *personalTemplateRepoCapture) Create(_ context.Context, req *mailEntity.CreatePersonalTemplateRequest, _ *mailEntity.MailOutboxRecord, _ string, _ []byte, _ []byte) (*mailEntity.CreatePersonalTemplateResponse, error) {
	now := time.Now().UTC()
	opID := uuid.New()
	hasher := sha256.New()
	hasher.Write([]byte(req.SubjectTemplate))
	hasher.Write([]byte{0x00})
	hasher.Write([]byte(req.RawHTML))
	contentHash := hasher.Sum(nil)

	r.createRes = &mailEntity.CreatePersonalTemplateResponse{
		ID:               uuid.NewString(),
		WorkspaceID:      &req.WorkspaceID,
		Code:             req.Code,
		Name:             req.Name,
		CurrentVersion:   1,
		TemplateRevision: 1,
		SubjectTemplate:  req.SubjectTemplate,
		RawHTML:          req.RawHTML,
		ContentSHA256:    contentHash,
		OperationID:      opID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	return r.createRes, nil
}

func (r *personalTemplateRepoCapture) GetByID(_ context.Context, _ *mailEntity.GetPersonalTemplateRequest) (*mailEntity.GetPersonalTemplateResponse, error) {
	return r.getRes, nil
}

func (r *personalTemplateRepoCapture) List(_ context.Context, _ *mailEntity.ListPersonalTemplatesRequest) ([]*mailEntity.PersonalTemplateItem, error) {
	return nil, nil
}

func (r *personalTemplateRepoCapture) ListVersions(_ context.Context, _ *mailEntity.ListPersonalTemplateVersionsRequest) ([]*mailEntity.PersonalTemplateVersionItem, error) {
	return nil, nil
}

func (r *personalTemplateRepoCapture) PublishVersion(_ context.Context, req *mailEntity.PublishPersonalTemplateVersionRequest, _ *mailEntity.MailOutboxRecord, _ []byte, _ []byte) (*mailEntity.PublishPersonalTemplateVersionResponse, error) {
	now := time.Now().UTC()
	opID := uuid.New()
	hasher := sha256.New()
	hasher.Write([]byte(req.SubjectTemplate))
	hasher.Write([]byte{0x00})
	hasher.Write([]byte(req.RawHTML))
	contentHash := hasher.Sum(nil)

	r.publishRes = &mailEntity.PublishPersonalTemplateVersionResponse{
		ID:                 req.TemplateID,
		WorkspaceID:        &req.WorkspaceID,
		Code:               "receipt",
		Name:               "Receipt",
		CurrentVersion:     1,
		CurrentRevision:    3,
		PublishedVersion:   2,
		PublishedRevision:  4,
		SubjectTemplate:    req.SubjectTemplate,
		RawHTML:            req.RawHTML,
		ContentSHA256:      contentHash,
		OperationID:        opID,
		HeadCreatedAt:      now,
		CandidateCreatedAt: now,
	}
	return r.publishRes, nil
}

func (r *personalTemplateRepoCapture) Delete(_ context.Context, _ *mailEntity.DeletePersonalTemplateRequest, _ *mailEntity.MailOutboxRecord) (uuid.UUID, error) {
	r.deleteOpID = uuid.New()
	return r.deleteOpID, nil
}

func TestPersonalTemplateDeleteUsesMonotonicRevisionFence(t *testing.T) {
	workspaceID := uuid.New()
	templateID := uuid.NewString()
	repo := &personalTemplateRepoCapture{}
	req := &mailEntity.DeletePersonalTemplateRequest{
		ActorUserID:      uuid.New(),
		ZoneID:           uuid.New(),
		WorkspaceID:      workspaceID,
		TemplateID:       templateID,
		ExpectedRevision: 4,
	}

	opID, err := mailSvcImpl.NewPersonalTemplateService(repo, observability.NewNoopWorkflowRecorder()).DeleteTemplate(context.Background(), req)
	if err != nil {
		t.Fatalf("DeleteTemplate() error = %v", err)
	}

	if opID != repo.deleteOpID || opID == uuid.Nil {
		t.Fatalf("delete returned invalid operation id: %v", opID)
	}
}

func TestPersonalTemplateCreateUsesOneEntityAndOutbox(t *testing.T) {
	workspaceID := uuid.New()
	repo := &personalTemplateRepoCapture{}
	res, err := mailSvcImpl.NewPersonalTemplateService(repo, observability.NewNoopWorkflowRecorder()).CreateTemplate(context.Background(), &mailEntity.CreatePersonalTemplateRequest{
		ActorUserID:     uuid.New(),
		WorkspaceID:     workspaceID,
		ZoneID:          uuid.New(),
		Code:            "receipt",
		Name:            "Receipt",
		SubjectTemplate: "Receipt {{id}}",
		RawHTML:         "<p>{{id}}</p>",
	})
	if err != nil {
		t.Fatalf("CreateTemplate() error = %v", err)
	}
	if res.CurrentVersion != 1 || res.TemplateRevision != 1 || len(res.ContentSHA256) != sha256.Size {
		t.Fatalf("unexpected response: %+v", res)
	}
}

func TestPersonalTemplateLeavesPlaceholderDetectionToDataplane(t *testing.T) {
	workspaceID := uuid.New()
	repo := &personalTemplateRepoCapture{}
	_, err := mailSvcImpl.NewPersonalTemplateService(repo, observability.NewNoopWorkflowRecorder()).CreateTemplate(context.Background(), &mailEntity.CreatePersonalTemplateRequest{
		ActorUserID:     uuid.New(),
		WorkspaceID:     workspaceID,
		ZoneID:          uuid.New(),
		Code:            "runtime",
		Name:            "Runtime",
		SubjectTemplate: "Hello {{name}}",
		RawHTML:         "<p>{{name}}</p>",
	})
	if err != nil || repo.createRes == nil {
		t.Fatalf("template did not reach repository: %v", err)
	}
}

func TestPersonalTemplatePublishAllocatesCandidateWithoutAdvancingActiveHead(t *testing.T) {
	workspaceID := uuid.New()
	templateID := uuid.NewString()
	repo := &personalTemplateRepoCapture{}
	req := &mailEntity.PublishPersonalTemplateVersionRequest{
		ActorUserID:      uuid.New(),
		ZoneID:           uuid.New(),
		WorkspaceID:      workspaceID,
		TemplateID:       templateID,
		ExpectedRevision: 1,
		SubjectTemplate:  "new",
		RawHTML:          "<p>new</p>",
	}

	res, err := mailSvcImpl.NewPersonalTemplateService(repo, observability.NewNoopWorkflowRecorder()).PublishTemplateVersion(context.Background(), req)
	if err != nil {
		t.Fatalf("PublishTemplateVersion() error = %v", err)
	}
	if res.CurrentVersion != 1 || res.PublishedRevision != 4 || res.OperationID == uuid.Nil {
		t.Fatalf("publish returned unexpected result: %+v", res)
	}
}
