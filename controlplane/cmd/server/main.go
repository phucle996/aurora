// ============================================================================
// 🛡️ ARCHITECTURAL & SYSTEM CONTRACTS
// ============================================================================
//
// 🤝 1. SYSTEM CONTRACT
//   - Bootstrap Entrypoint: Điểm khởi đầu duy nhất (Main Entrypoint) của hệ thống Controlplane.
//     Chịu trách nhiệm nạp cấu hình, đồng bộ múi giờ, khởi tạo logger và khởi động container.
//   - Graceful Shutdown: Đăng ký lắng nghe tín hiệu chấm dứt từ hệ điều hành (SIGINT, SIGTERM).
//     Khi nhận tín hiệu, hệ thống kích hoạt chuỗi đóng dịch vụ tuần tự (dừng nhận kết nối mới, giải phóng
//     các connection pool của Database/Redis) để đảm bảo không thất thoát dữ liệu và an toàn phiên làm việc.
//
// 📖 2. SOURCE OF TRUTH
//   - Cấu hình runtime của toàn bộ hệ thống được nạp duy nhất từ config.LoadConfig() làm nguồn tin cậy.
//
// 🚧 3. SYSTEM BOUNDARY
//   - Đóng vai trò là lớp vỏ bọc ngoài cùng tiếp xúc trực tiếp với OS Kernel. Khởi chạy luồng mạng lắng nghe
//     và thu hồi tài nguyên trực tiếp trên máy chủ vật lý hoặc container chạy độc lập.
//
// 💡 4. OPERATIONAL NOTES
//   - Đồng bộ thời gian: Đồng bộ đồng nhất biến cục bộ time.Local theo múi giờ cấu hình để đảm bảo các bản ghi
//     audit log và các tác vụ theo giờ (cron/schedule) hoạt động chính xác.
//   - Chạy nền bất đồng bộ: Kích hoạt ứng dụng trong một Goroutine riêng biệt để không chặn luồng chính,
//     giúp luồng chính luôn túc trực lắng nghe và phản hồi tức thì với tín hiệu shutdown của SRE/Orchestrator.

package main

import (
	"controlplane/pkg/logger"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"controlplane/internal/app"
	"controlplane/internal/config"
)

func main() {
	// --------------------------------------------------------------------
	// 🔄 Nạp cấu hình hệ thống từ môi trường (Environment variables / Config file).
	// --------------------------------------------------------------------
	cfg, err := config.LoadConfig()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "controlplane configuration error: %v\n", err)
		os.Exit(1)
	}
	logger.InitLogger(cfg.App.AppName)

	// --------------------------------------------------------------------
	// 🔄 Thiết lập múi giờ (Timezone) hệ thống nhất quán toàn cục.
	// --------------------------------------------------------------------
	loc, err := time.LoadLocation(cfg.App.TimeZone)
	if err != nil {
		logger.SysWarn("main", "Failed to load timezone from environment variable "+cfg.App.TimeZone+": "+err.Error())
		time.Local = time.UTC
	} else {
		time.Local = loc
	}

	// --------------------------------------------------------------------
	// 🔄 Khởi tạo bộ khung Application Container (Dependency Injection).
	// --------------------------------------------------------------------
	application, err := app.NewApplication(cfg)
	if err != nil {
		logger.SysFatal("main", "Failed to initialize application: "+err.Error())
	}

	// --------------------------------------------------------------------
	// 🔄 Đăng ký kênh lắng nghe tín hiệu ngắt (SIGINT / SIGTERM) của Hệ điều hành.
	// --------------------------------------------------------------------
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	// --------------------------------------------------------------------
	// 🔄 Khởi động Application Server bất đồng bộ trong một Goroutine riêng biệt.
	// --------------------------------------------------------------------
	go func() {
		if err := application.Start(); err != nil {
			logger.SysFatal("main", "Application failed to start: "+err.Error())
		}
	}()

	// Chặn luồng chính tại đây để chờ tín hiệu kết thúc từ OS:
	<-stop

	// --------------------------------------------------------------------
	// 🔄 Thu hồi tài nguyên và giải phóng kết nối an toàn (Graceful Shutdown).
	// --------------------------------------------------------------------
	application.Stop()
	logger.SysInfo("main", "Application stopped gracefully.")
}
