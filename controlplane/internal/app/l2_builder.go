package app

import (
	"context"
	"time"

	"controlplane/internal/cacheengine"
)

// RegisterL2Loaders đăng ký toàn bộ các cache loaders L2 sử dụng các module nghiệp vụ đã wire hoàn chỉnh.
func RegisterL2Loaders(
	registry *cacheengine.CacheRegistry,
	modules *Modules,
) {
	// Đăng ký loader cho "admin_public_key" sử dụng cacheEngine L2 và AdminAPIKeyRepository.
	cacheengine.Register(registry, "admin_public_key", 5*time.Minute, func(ctx context.Context, accessKey string) (string, error) {
		var pubKey string
		_, err := registry.L2.GetOrLoad(ctx, "admin_public_key:"+accessKey, &pubKey, 5*time.Minute, func() (interface{}, error) {
			return modules.IAM.AdminAPIKeyService.GetPublicKeyFromSession(ctx, accessKey)
		})
		return pubKey, err
	})
}
