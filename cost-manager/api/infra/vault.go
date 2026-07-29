package infra

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"cost-manager/api/internal/config"
)

type VaultClient struct {
	httpClient *http.Client
	addr       string
	token      string
	maxRetries int
}

type vaultKVResponse struct {
	Data struct {
		Data json.RawMessage `json:"data"`
	} `json:"data"`
}

func NewVaultClient(ctx context.Context, cfg config.VaultCfg) (*VaultClient, error) {
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
	client := &http.Client{Timeout: timeout}
	var lastErr error
	for attempt := 1; attempt <= retries; attempt++ {
		token, err := authenticateVault(ctx, client, cfg)
		if err == nil {
			return &VaultClient{
				httpClient: client,
				addr:       strings.TrimRight(cfg.Addr, "/"),
				token:      token,
				maxRetries: retries,
			}, nil
		}
		lastErr = err
		if attempt < retries {
			timer := time.NewTimer(time.Duration(attempt) * 250 * time.Millisecond)
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

func (c *VaultClient) ReadJSON(ctx context.Context, path string, dst any) error {
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
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.addr+"/v1/"+strings.TrimLeft(path, "/"), nil)
		if err != nil {
			return fmt.Errorf("vault: create read request: %w", err)
		}
		req.Header.Set("X-Vault-Token", c.token)
		resp, err := c.httpClient.Do(req)
		if err == nil {
			if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
				var payload vaultKVResponse
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

func authenticateVault(ctx context.Context, client *http.Client, cfg config.VaultCfg) (string, error) {
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.Addr, "/")+"/v1/"+path, bytes.NewReader(body))
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
