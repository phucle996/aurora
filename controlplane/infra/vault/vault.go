package vault

import (
	"context"
	"controlplane/internal/config"
	"errors"
	"fmt"
	"time"

	vaultapi "github.com/hashicorp/vault/api"
)

// [COMMENT]: NewVaultClient khởi tạo một instance Vault API Client kết nối trực tiếp đến Vault Server.
// Hàm này thực hiện cơ chế Retry thông qua cấu hình MaxRetries để đảm bảo tính chịu lỗi (Fault-Tolerance)
// và tính sẵn sàng cao (HA) trong môi trường microservices.
func NewVaultClient(ctx context.Context, cfg *config.VaultCfg) (*vaultapi.Client, error) {
	// [COMMENT]: Khởi tạo cấu hình mặc định của Vault Client SDK
	vaultCfg := vaultapi.DefaultConfig()
	vaultCfg.Address = cfg.Addr
	vaultCfg.Timeout = cfg.Timeout

	var client *vaultapi.Client
	var err error

	// [COMMENT]: Thực hiện kết nối và kiểm tra tính sẵn sàng của Vault với số lần thử lại cấu hình trước
	for attempt := 1; attempt <= cfg.MaxRetries; attempt++ {
		client, err = vaultapi.NewClient(vaultCfg)
		if err != nil {
			if attempt < cfg.MaxRetries {
				time.Sleep(1 * time.Second)
			}
			continue
		}

		// [COMMENT]: Thiết lập Token để xác thực các yêu cầu sau này gửi tới Vault
		client.SetToken(cfg.Token)

		// [COMMENT]: Kiểm tra trạng thái hoạt động thực tế (Healthcheck) của Vault Server.
		// Đảm bảo Vault đã được khởi tạo (Initialized) và đã được mở khóa (Unsealed) trước khi trả về client hợp lệ.
		var health *vaultapi.HealthResponse
		health, err = client.Sys().Health()
		if err == nil {
			if !health.Initialized {
				err = errors.New("vault is not initialized yet")
			} else if health.Sealed {
				err = errors.New("vault is sealed")
			} else {
				// [COMMENT]: Vault hoạt động bình thường, trả về Client đã kết nối thành công
				return client, nil
			}
		}

		// [COMMENT]: Giải phóng tài nguyên hoặc chuẩn bị thử lại nếu có lỗi xảy ra
		if attempt < cfg.MaxRetries {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(1 * time.Second):
			}
		}
	}

	return nil, fmt.Errorf("vault: failed to establish healthy connection after %d attempts: %w", cfg.MaxRetries, err)
}
