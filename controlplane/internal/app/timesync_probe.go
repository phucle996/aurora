package app

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"controlplane/internal/observability"
	"controlplane/pkg/logger"
)

type TimeDriftState string

const (
	TimeDriftOK       TimeDriftState = "ok"
	TimeDriftWarning  TimeDriftState = "warning"
	TimeDriftCritical TimeDriftState = "critical"
	TimeDriftUnknown  TimeDriftState = "unknown"
)

type TimeDriftSnapshot struct {
	// Seconds là độ lệch tuyệt đối theo giây so với nguồn chrony parse được.
	Seconds float64
	// State là phân loại drift state phục vụ health/metrics/alerts.
	State TimeDriftState
	// Checked là timestamp lần đo gần nhất (UTC).
	Checked time.Time
}

// TimeSyncProbe là read-only runtime probe cho time drift observability.
//
// Probe này KHÔNG chỉnh clock hệ điều hành.
// Nó chỉ:
// - đọc drift từ chronyc output,
// - map state,
// - publish tín hiệu cho metrics/health.
type TimeSyncProbe struct {
	mu       sync.RWMutex
	snapshot TimeDriftSnapshot
}

// NewTimeSyncProbe khởi tạo probe với state mặc định unknown.
func NewTimeSyncProbe() *TimeSyncProbe {
	return &TimeSyncProbe{snapshot: TimeDriftSnapshot{State: TimeDriftUnknown}}
}

// Start chạy vòng lặp probe định kỳ 30 giây cho tới khi context bị cancel.
//
// Lưu ý:
// - đây là background loop, không nằm request hot path.
// - tick() chịu trách nhiệm parse + emit metrics + state-change log.
func (p *TimeSyncProbe) Start(ctx context.Context) {
	const op = "app.timesync.probe"
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		p.tick()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		_ = op
	}
}

// Snapshot trả về snapshot drift mới nhất (copy by value).
func (p *TimeSyncProbe) Snapshot() TimeDriftSnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.snapshot
}

// tick thực hiện 1 lần đo drift:
// 1) gọi `chronyc tracking`,
// 2) parse offset,
// 3) map state,
// 4) cập nhật snapshot,
// 5) emit metrics,
// 6) log khi state thay đổi.
func (p *TimeSyncProbe) tick() {
	out, err := exec.Command("chronyc", "tracking").CombinedOutput()
	snap := TimeDriftSnapshot{Checked: time.Now().UTC(), State: TimeDriftUnknown}
	if err == nil {
		if secs, ok := parseChronyTracking(string(out)); ok {
			snap.Seconds = secs
			snap.State = mapDriftState(secs)
		}
	}
	p.mu.Lock()
	prev := p.snapshot.State
	p.snapshot = snap
	p.mu.Unlock()

	if prom := observability.CurrentMetrics(); prom != nil {
		prom.ObserveTimeDrift(snap.Seconds, string(snap.State))
	}
	if prev != snap.State {
		logger.SysInfo("app.timesync.probe", fmt.Sprintf("time drift state changed: %s -> %s", prev, snap.State))
	}
}

var reSeconds = regexp.MustCompile(`([-+]?\d+\.?\d*)\s+seconds`)

func parseChronyTracking(out string) (float64, bool) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "system time") || strings.HasPrefix(strings.ToLower(line), "last offset") {
			m := reSeconds.FindStringSubmatch(line)
			if len(m) < 2 {
				continue
			}
			v, err := strconv.ParseFloat(strings.TrimSpace(m[1]), 64)
			if err != nil {
				continue
			}
			if v < 0 {
				v = -v
			}
			return v, true
		}
	}
	return 0, false
}

// mapDriftState ánh xạ drift seconds sang health state theo baseline:
// - >2s      => critical
// - >0.5s    => warning
// - còn lại  => ok
func mapDriftState(secs float64) TimeDriftState {
	if secs > 2 {
		return TimeDriftCritical
	}
	if secs > 0.5 {
		return TimeDriftWarning
	}
	return TimeDriftOK
}
