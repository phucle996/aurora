package apires

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestStableTaxonomyResponsesPreserveStatusCodeAndEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		statusCode int
		code       string
		respond    func(*gin.Context)
	}{
		{name: "bad_request", statusCode: http.StatusBadRequest, code: "bad request", respond: func(c *gin.Context) {
			RespondBadRequest(c, "invalid request")
		}},
		{name: "not_found", statusCode: http.StatusNotFound, code: "not found", respond: func(c *gin.Context) {
			RespondNotFound(c, "catalog not found")
		}},
		{name: "conflict", statusCode: http.StatusConflict, code: "conflict", respond: func(c *gin.Context) {
			RespondConflict(c, "catalog stale")
		}},
		{name: "unprocessable", statusCode: http.StatusUnprocessableEntity, code: "CATALOG_VALIDATION_FAILED", respond: func(c *gin.Context) {
			RespondUnprocessableEntity(c, "CATALOG_VALIDATION_FAILED", "catalog invalid")
		}},
		{name: "unavailable", statusCode: http.StatusServiceUnavailable, code: "CATALOG_UNAVAILABLE", respond: func(c *gin.Context) {
			RespondServiceUnavailable(c, "CATALOG_UNAVAILABLE")
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/", test.respond)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

			if response.Code != test.statusCode {
				t.Fatalf("expected status %d, got %d", test.statusCode, response.Code)
			}
			var envelope APIResponse
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if envelope.Error != test.code || envelope.Message == "" || envelope.Data != nil {
				t.Fatalf("unexpected envelope: %#v", envelope)
			}
		})
	}
}
