package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingSvcInterface "cost-manager/api/internal/domain/service"
	"cost-manager/api/internal/transport/http/handler"
	"cost-manager/api/pkg/pkgcontext"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type resourcePlanHandlerServiceStub struct {
	billingSvcInterface.HypervisorResourcePlanService
	called  bool
	create  entity.CreateHypervisorResourcePlanCommand
	publish entity.PublishHypervisorResourcePlanRevisionCommand
}

func (s *resourcePlanHandlerServiceStub) CreateHypervisorResourcePlan(_ context.Context, command entity.CreateHypervisorResourcePlanCommand) (*entity.HypervisorResourcePlanRevision, error) {
	s.called = true
	s.create = command
	return &entity.HypervisorResourcePlanRevision{PlanID: uuid.New(), RevisionID: uuid.New(), RevisionNumber: 1, EffectiveFrom: command.EffectiveFrom}, nil
}
func (s *resourcePlanHandlerServiceStub) PublishHypervisorResourcePlanRevision(_ context.Context, command entity.PublishHypervisorResourcePlanRevisionCommand) (*entity.HypervisorResourcePlanRevision, error) {
	s.called = true
	s.publish = command
	return &entity.HypervisorResourcePlanRevision{PlanID: command.PlanID, RevisionID: uuid.New(), RevisionNumber: command.ExpectedLatestRevision + 1, EffectiveFrom: command.EffectiveFrom}, nil
}
func (*resourcePlanHandlerServiceStub) NotifyHypervisorResourcePlanOutbox() {}

func TestResourcePlanTransportRejectsInvalidBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, publish := range []bool{false, true} {
		for _, bad := range []string{"missing-time", "null-time", "zero-time", "normalized-zero-time", "bad-time", "trailing-json", "fraction", "overflow", "negative", "empty-reason", "overflow-revision"} {
			if !publish && bad == "overflow-revision" {
				continue
			}
			t.Run(strings.Join([]string{map[bool]string{true: "publish", false: "create"}[publish], bad}, "/"), func(t *testing.T) {
				body := map[string]any{"cpu_cores": "2", "memory_mib": "4096", "boot_disk_gib": "64", "effective_from": "2026-09-01T12:00:00+07:00", "change_reason": "initial"}
				if publish {
					body["expected_latest_revision"] = "1"
				} else {
					body["code"] = "compute.standard"
					body["display_name"] = "Standard"
				}
				switch bad {
				case "missing-time":
					delete(body, "effective_from")
				case "null-time":
					body["effective_from"] = nil
				case "zero-time":
					body["effective_from"] = "0001-01-01T00:00:00Z"
				case "normalized-zero-time":
					body["effective_from"] = "0001-01-01T00:00:00.0000001Z"
				case "bad-time":
					body["effective_from"] = "tomorrow"
				case "fraction":
					body["memory_mib"] = "1.5"
				case "overflow":
					body["boot_disk_gib"] = "9223372036854775808"
				case "negative":
					body["cpu_cores"] = "-1"
				case "empty-reason":
					body["change_reason"] = " "
				case "overflow-revision":
					body["expected_latest_revision"] = "9223372036854775807"
				}
				payload, _ := json.Marshal(body)
				if bad == "trailing-json" {
					payload = append(payload, []byte(" {}")...)
				}
				stub := &resourcePlanHandlerServiceStub{}
				h := handler.NewHypervisorResourcePlanHandler(stub)
				router := gin.New()
				router.Use(func(c *gin.Context) { c.Set(pkgcontext.CtxUserID, uuid.New()); c.Next() })
				path := "/plans"
				if publish {
					router.POST("/plans/:plan_id/revisions", h.PublishRevision)
					path += "/" + uuid.NewString() + "/revisions"
				} else {
					router.POST(path, h.Create)
				}
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(payload))))
				if response.Code != 400 || stub.called {
					t.Fatalf("invalid input reached service: %d %s called=%v", response.Code, response.Body, stub.called)
				}
			})
		}
	}
}

func TestResourcePlanTransportNormalizesBeforeService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &resourcePlanHandlerServiceStub{}
	h := handler.NewHypervisorResourcePlanHandler(stub)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set(pkgcontext.CtxUserID, uuid.New()); c.Next() })
	router.POST("/plans", h.Create)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("POST", "/plans", strings.NewReader(`{"code":" COMPUTE.STANDARD ","display_name":" Standard ","cpu_cores":"2","memory_mib":"4096","boot_disk_gib":"65536","effective_from":"2026-09-01T12:00:00.123456789+07:00","change_reason":" initial "}`)))
	if response.Code != 201 {
		t.Fatalf("%d %s", response.Code, response.Body)
	}
	if stub.create.Code != "compute.standard" || stub.create.ChangeReason != "initial" || stub.create.EffectiveFrom.Location() != time.UTC || stub.create.EffectiveFrom.Nanosecond() != 123456000 {
		t.Fatalf("not normalized: %#v", stub.create)
	}
}
