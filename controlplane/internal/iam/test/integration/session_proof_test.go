package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"controlplane/internal/http/middleware"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestRequireSessionProofFailsClosedAndAcceptsOnlyACRMarker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.RequireSessionProof())
	router.POST("/critical", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	tests := []struct {
		name      string
		verified  string
		challenge string
		status    int
	}{
		{name: "missing marker", challenge: uuid.NewString(), status: http.StatusForbidden},
		{name: "invalid challenge", verified: "true", challenge: "not-a-uuid", status: http.StatusForbidden},
		{name: "valid ACR proof", verified: "true", challenge: uuid.NewString(), status: http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/critical", nil)
			request.Header.Set("x-session-proof-verified", test.verified)
			request.Header.Set("x-session-proof-challenge-id", test.challenge)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("expected status %d, got %d", test.status, recorder.Code)
			}
		})
	}
}
