package iamSvcImpl

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/observability"
)

type RenderContextService struct {
	repo    iamRepoInterface.RenderContextRepository
	metrics observability.WorkflowRecorder
}

func NewRenderContextService(
	repo iamRepoInterface.RenderContextRepository,
	metrics observability.WorkflowRecorder,
) iamSvcInterface.RenderContextService {
	return &RenderContextService{repo: repo, metrics: metrics}
}

func (s *RenderContextService) GetPersonalRenderContext(ctx context.Context, workflow *iamEntity.PersonalRenderContext) (err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			result, reason = observability.ResultRejected, observability.ReasonForbidden
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	if err := s.repo.GetPersonalRenderContext(ctx, workflow); err != nil {
		return err
	}

	capabilitySet := make(map[string]struct{}, len(workflow.Permissions))
	navigationSet := make(map[string]struct{}, len(workflow.Permissions))
	for _, permission := range workflow.Permissions {
		parts := strings.Split(permission, ":")
		capabilitySet[permission] = struct{}{}
		navigationSet[parts[2]+":"+parts[3]+"\x00"+parts[4]] = struct{}{}
	}
	workflow.Capabilities = make([]string, 0, len(capabilitySet))
	for permission := range capabilitySet {
		workflow.Capabilities = append(workflow.Capabilities, permission)
	}
	sort.Strings(workflow.Capabilities)
	navigation := make([]string, 0, len(navigationSet))
	for entry := range navigationSet {
		navigation = append(navigation, entry)
	}
	sort.Strings(navigation)
	workflow.NavigationKeys = make([]string, 0, len(navigation))
	workflow.NavigationActions = make([]string, 0, len(navigation))
	for _, entry := range navigation {
		parts := strings.SplitN(entry, "\x00", 2)
		workflow.NavigationKeys = append(workflow.NavigationKeys, parts[0])
		workflow.NavigationActions = append(workflow.NavigationActions, parts[1])
	}
	return nil
}

func (s *RenderContextService) GetTenantRenderContext(ctx context.Context, workflow *iamEntity.TenantRenderContext) (err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			result, reason = observability.ResultRejected, observability.ReasonForbidden
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	if err := s.repo.GetTenantRenderContext(ctx, workflow); err != nil {
		return err
	}

	capabilitySet := make(map[string]struct{}, len(workflow.Permissions))
	navigationSet := make(map[string]struct{}, len(workflow.Permissions))
	for _, permission := range workflow.Permissions {
		parts := strings.Split(permission, ":")
		capabilitySet[permission] = struct{}{}
		navigationSet[parts[2]+":"+parts[3]+"\x00"+parts[4]] = struct{}{}
	}
	workflow.Capabilities = make([]string, 0, len(capabilitySet))
	for permission := range capabilitySet {
		workflow.Capabilities = append(workflow.Capabilities, permission)
	}
	sort.Strings(workflow.Capabilities)
	navigation := make([]string, 0, len(navigationSet))
	for entry := range navigationSet {
		navigation = append(navigation, entry)
	}
	sort.Strings(navigation)
	workflow.NavigationKeys = make([]string, 0, len(navigation))
	workflow.NavigationActions = make([]string, 0, len(navigation))
	for _, entry := range navigation {
		parts := strings.SplitN(entry, "\x00", 2)
		workflow.NavigationKeys = append(workflow.NavigationKeys, parts[0])
		workflow.NavigationActions = append(workflow.NavigationActions, parts[1])
	}
	return nil
}
