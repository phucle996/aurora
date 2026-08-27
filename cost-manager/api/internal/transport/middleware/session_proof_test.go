package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestRequireSessionProofFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/critical", RequireSessionProof(), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	// Case 1: Thiếu hoàn toàn header
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/critical", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected missing session proof to be forbidden, got %d", response.Code)
	}

	// Case 2: Header verified = "false"
	req2 := httptest.NewRequest(http.MethodPost, "/critical", nil)
	req2.Header.Set("x-session-proof-verified", "false")
	req2.Header.Set("x-session-proof-challenge-id", uuid.NewString())
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("expected unverified session proof to be forbidden, got %d", rec2.Code)
	}

	// Case 3: Header verified = "true" nhưng challenge ID rỗng / không hợp lệ
	req3 := httptest.NewRequest(http.MethodPost, "/critical", nil)
	req3.Header.Set("x-session-proof-verified", "true")
	req3.Header.Set("x-session-proof-challenge-id", "invalid-uuid")
	rec3 := httptest.NewRecorder()
	router.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusForbidden {
		t.Fatalf("expected invalid challenge id to be forbidden, got %d", rec3.Code)
	}

	// Case 4: Đầy đủ và hợp lệ
	req4 := httptest.NewRequest(http.MethodPost, "/critical", nil)
	req4.Header.Set("x-session-proof-verified", "true")
	req4.Header.Set("x-session-proof-challenge-id", uuid.NewString())
	rec4 := httptest.NewRecorder()
	router.ServeHTTP(rec4, req4)
	if rec4.Code != http.StatusNoContent {
		t.Fatalf("expected valid session proof to pass, got %d", rec4.Code)
	}
}
