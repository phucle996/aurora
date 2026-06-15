package middleware_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/http/middleware"
	"controlplane/pkg/constant"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"
)

func TestAdminCriticalSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)

	pubKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	deviceID := "admin-device-1"
	pubKeyEncoded := base64.StdEncoding.EncodeToString(pubKey)

	redisServer := miniredis.RunT(t)
	rds := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = rds.Close() })

	registry := cacheengine.NewCacheRegistry(cacheengine.NewL1Cache())
	registry.L2 = cacheengine.NewL2Cache(rds)
	registry.Exec = cacheengine.NewL2LuaExecutor(rds)
	pubKeyFn := func(ctx context.Context, gotDeviceID string) (string, error) {
		if gotDeviceID != deviceID {
			return "", fmt.Errorf("device id = %q, want %q", gotDeviceID, deviceID)
		}
		return pubKeyEncoded, nil
	}

	if err := middleware.InitAdminCriticalSignature(registry, time.Minute, time.Minute, pubKeyFn); err != nil {
		t.Fatalf("init signature guard: %v", err)
	}

	body := `{"name":"zone-a"}`
	router := gin.New()
	router.POST("/admin/critical",
		func(c *gin.Context) {
			ident := &constant.Identity{AccessKey: deviceID}
			ctx := context.WithValue(c.Request.Context(), constant.IdentityKey, ident)
			c.Request = c.Request.WithContext(ctx)
		},
		middleware.AdminCriticalSignature(),
		func(c *gin.Context) {
			bodyRaw, readErr := io.ReadAll(c.Request.Body)
			if readErr != nil {
				t.Fatalf("read restored body: %v", readErr)
			}
			if string(bodyRaw) != body {
				t.Fatalf("restored body = %q, want %q", string(bodyRaw), body)
			}
			c.Status(http.StatusNoContent)
		},
	)

	req := newSignedCriticalRequest(t, http.MethodPost, "/admin/critical?sort=asc", body, "nonce-valid", privateKey, time.Now())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("valid signature status = %d, want %d", rec.Code, http.StatusNoContent)
	}

	replayReq := newSignedCriticalRequest(t, http.MethodPost, "/admin/critical?sort=asc", body, "nonce-valid", privateKey, time.Now())
	replayRec := httptest.NewRecorder()
	router.ServeHTTP(replayRec, replayReq)
	if replayRec.Code != http.StatusUnauthorized {
		t.Fatalf("replay status = %d, want %d", replayRec.Code, http.StatusUnauthorized)
	}

	badReq := newSignedCriticalRequest(t, http.MethodPost, "/admin/critical?sort=asc", body, "nonce-invalid-then-valid", privateKey, time.Now())
	badReq.Header.Set(constant.HeaderAdminSignature, base64.StdEncoding.EncodeToString([]byte("invalid-signature")))
	badRec := httptest.NewRecorder()
	router.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid signature status = %d, want %d", badRec.Code, http.StatusUnauthorized)
	}

	validAfterBadReq := newSignedCriticalRequest(t, http.MethodPost, "/admin/critical?sort=asc", body, "nonce-invalid-then-valid", privateKey, time.Now())
	validAfterBadRec := httptest.NewRecorder()
	router.ServeHTTP(validAfterBadRec, validAfterBadReq)
	if validAfterBadRec.Code != http.StatusNoContent {
		t.Fatalf("valid after invalid signature status = %d, want %d", validAfterBadRec.Code, http.StatusNoContent)
	}
}

func newSignedCriticalRequest(
	t *testing.T,
	method string,
	target string,
	body string,
	nonce string,
	privateKey ed25519.PrivateKey,
	now time.Time,
) *http.Request {
	t.Helper()

	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse target: %v", err)
	}
	tsRaw := strconv.FormatInt(now.Unix(), 10)
	bodyHash := sha256.Sum256([]byte(body))
	payload := fmt.Sprintf("%s\n%s\n%s\n%x\n%s\n%s",
		method,
		parsed.Path,
		parsed.RawQuery,
		bodyHash,
		tsRaw,
		nonce,
	)
	signature := ed25519.Sign(privateKey, []byte(payload))

	req := httptest.NewRequest(method, target, bytes.NewReader([]byte(body)))
	req.Header.Set(constant.HeaderAdminTimestamp, tsRaw)
	req.Header.Set(constant.HeaderAdminNonce, nonce)
	req.Header.Set(constant.HeaderAdminSignature, base64.StdEncoding.EncodeToString(signature))
	return req
}
