package vault

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"controlplane/internal/config"
)

// Client is the process-scoped Vault client. It authenticates once during
// bootstrap and keeps the token private; connectors only receive typed data
// from ReadJSON and never handle Vault authentication details.
type Client struct {
	httpClient *http.Client
	addr       string
	token      string
	maxRetries int
}

type kvResponse struct {
	Data struct {
		Data json.RawMessage `json:"data"`
	} `json:"data"`
}

// NewClient authenticates with the app's Vault identity. A static token is
// accepted only for local bootstrap; production should use Kubernetes auth or
// AppRole so each workload has an independent least-privilege policy.
func NewClient(ctx context.Context, cfg config.VaultCfg) (*Client, error) {
	if strings.TrimSpace(cfg.Addr) == "" {
		return nil, errors.New("vault: address is required")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	retries := cfg.MaxRetries
	if retries < 1 {
		retries = 1
	}
	httpClient := &http.Client{Timeout: timeout}
	var lastErr error
	for attempt := 1; attempt <= retries; attempt++ {
		token, err := authenticate(ctx, httpClient, cfg)
		if err == nil {
			return &Client{
				httpClient: httpClient,
				addr:       strings.TrimRight(cfg.Addr, "/"),
				token:      token,
				maxRetries: retries,
			}, nil
		}
		lastErr = err
		if attempt < retries {
			delay := time.Duration(attempt) * 250 * time.Millisecond
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, fmt.Errorf("vault: authentication cancelled: %w", ctx.Err())
			case <-timer.C:
			}
		}
	}
	return nil, fmt.Errorf("vault: authentication failed after %d attempts: %w", retries, lastErr)
}

// ReadJSON reads one KV-v2 record. The caller supplies only a fixed,
// connector-owned path; request data never controls this path.
func (c *Client) ReadJSON(ctx context.Context, path string, dst any) error {
	if c == nil || c.httpClient == nil {
		return errors.New("vault: client is required")
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("vault: secret path is required")
	}
	retries := c.maxRetries
	if retries < 1 {
		retries = 1
	}
	var lastErr error
	for attempt := 1; attempt <= retries; attempt++ {
		req, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			c.addr+"/v1/"+strings.TrimLeft(path, "/"),
			nil,
		)
		if err != nil {
			return fmt.Errorf("vault: create read request: %w", err)
		}
		req.Header.Set("X-Vault-Token", c.token)
		resp, err := c.httpClient.Do(req)
		if err == nil {
			if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
				var payload kvResponse
				decodeErr := json.NewDecoder(resp.Body).Decode(&payload)
				resp.Body.Close()
				if decodeErr != nil {
					return fmt.Errorf("vault: decode KV response: %w", decodeErr)
				}
				if len(payload.Data.Data) == 0 || string(payload.Data.Data) == "null" {
					return errors.New("vault: KV record has no data")
				}
				if err := json.Unmarshal(payload.Data.Data, dst); err != nil {
					return fmt.Errorf("vault: decode KV record data: %w", err)
				}
				return nil
			}
			statusCode := resp.StatusCode
			resp.Body.Close()
			// Authentication/authorization/schema paths are deterministic and
			// fail immediately. Only throttling and server faults are retried.
			if statusCode != http.StatusTooManyRequests && statusCode < http.StatusInternalServerError {
				return fmt.Errorf("vault: read returned HTTP %d", statusCode)
			}
			lastErr = fmt.Errorf("vault: read returned HTTP %d", statusCode)
		} else {
			lastErr = fmt.Errorf("vault: read request: %w", err)
		}
		if attempt < retries {
			timer := time.NewTimer(time.Duration(attempt) * 250 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return fmt.Errorf("vault: read cancelled: %w", ctx.Err())
			case <-timer.C:
			}
		}
	}
	return fmt.Errorf("vault: read failed after %d attempts: %w", retries, lastErr)
}

// TransitEncrypt delegates encryption to Vault Transit. The plaintext is only
// present in the request body and the returned versioned ciphertext is safe to
// persist; the key material never crosses the Vault boundary.
func (c *Client) TransitEncrypt(ctx context.Context, keyPath, plaintext string) (string, error) {
	if strings.TrimSpace(plaintext) == "" {
		return "", errors.New("vault: transit plaintext is required")
	}
	key := strings.Trim(strings.TrimSpace(keyPath), "/")
	if key == "" || strings.Contains(key, "..") {
		return "", errors.New("vault: invalid transit key path")
	}
	body, err := json.Marshal(map[string]string{
		"plaintext": base64.StdEncoding.EncodeToString([]byte(plaintext)),
	})
	if err != nil {
		return "", fmt.Errorf("vault: encode transit request: %w", err)
	}
	var payload struct {
		Data struct {
			Ciphertext string `json:"ciphertext"`
		} `json:"data"`
	}
	if err := c.transitRequest(ctx, http.MethodPost, "transit/encrypt/"+key, body, &payload); err != nil {
		return "", err
	}
	if strings.TrimSpace(payload.Data.Ciphertext) == "" {
		return "", errors.New("vault: transit encrypt returned empty ciphertext")
	}
	return payload.Data.Ciphertext, nil
}

// TransitDecrypt decrypts a versioned ciphertext in Vault. Vault selects the
// ciphertext's key version, allowing key rotation without rewriting all rows.
func (c *Client) TransitDecrypt(ctx context.Context, keyPath, ciphertext string) (string, error) {
	key := strings.Trim(strings.TrimSpace(keyPath), "/")
	if key == "" || strings.Contains(key, "..") || strings.TrimSpace(ciphertext) == "" {
		return "", errors.New("vault: invalid transit decrypt request")
	}
	body, err := json.Marshal(map[string]string{"ciphertext": strings.TrimSpace(ciphertext)})
	if err != nil {
		return "", fmt.Errorf("vault: encode transit decrypt request: %w", err)
	}
	var payload struct {
		Data struct {
			Plaintext string `json:"plaintext"`
		} `json:"data"`
	}
	if err := c.transitRequest(ctx, http.MethodPost, "transit/decrypt/"+key, body, &payload); err != nil {
		return "", err
	}
	plain, err := base64.StdEncoding.DecodeString(payload.Data.Plaintext)
	if err != nil {
		return "", fmt.Errorf("vault: decode transit plaintext: %w", err)
	}
	if len(plain) == 0 {
		return "", errors.New("vault: transit decrypt returned empty plaintext")
	}
	return string(plain), nil
}

func (c *Client) transitRequest(ctx context.Context, method, path string, body []byte, dst any) error {
	if c == nil || c.httpClient == nil {
		return errors.New("vault: client is required")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.addr+"/v1/"+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("vault: create transit request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Vault-Token", c.token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("vault: transit request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("vault: transit returned HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("vault: decode transit response: %w", err)
	}
	return nil
}

func authenticate(ctx context.Context, client *http.Client, cfg config.VaultCfg) (string, error) {
	if token := strings.TrimSpace(cfg.Token); token != "" {
		return token, nil
	}

	var path string
	var body []byte
	switch {
	case strings.TrimSpace(cfg.RoleID) != "" && strings.TrimSpace(cfg.SecretID) != "":
		path = "auth/approle/login"
		var err error
		body, err = json.Marshal(map[string]string{
			"role_id":   strings.TrimSpace(cfg.RoleID),
			"secret_id": strings.TrimSpace(cfg.SecretID),
		})
		if err != nil {
			return "", fmt.Errorf("vault: encode AppRole login: %w", err)
		}
	case strings.TrimSpace(cfg.KubernetesRole) != "":
		jwt, err := os.ReadFile(cfg.KubernetesJWTPath)
		if err != nil {
			return "", fmt.Errorf("vault: read Kubernetes JWT: %w", err)
		}
		path = "auth/kubernetes/login"
		body, err = json.Marshal(map[string]string{
			"role": strings.TrimSpace(cfg.KubernetesRole),
			"jwt":  strings.TrimSpace(string(jwt)),
		})
		if err != nil {
			return "", fmt.Errorf("vault: encode Kubernetes login: %w", err)
		}
	default:
		return "", errors.New("vault: no token, AppRole, or Kubernetes auth configured")
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		strings.TrimRight(cfg.Addr, "/")+"/v1/"+path,
		bytes.NewReader(body),
	)
	if err != nil {
		return "", fmt.Errorf("vault: create auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("vault: auth request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("vault: auth returned HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Auth struct {
			ClientToken string `json:"client_token"`
		} `json:"auth"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("vault: decode auth response: %w", err)
	}
	if strings.TrimSpace(payload.Auth.ClientToken) == "" {
		return "", errors.New("vault: auth returned an empty client token")
	}
	return payload.Auth.ClientToken, nil
}
