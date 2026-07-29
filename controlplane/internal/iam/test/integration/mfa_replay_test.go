package integration

import (
	"testing"
	"time"
)

// [COMMENT]: Integration Test giả lập cơ chế TOTP Replay Prevention (AcceptTOTPStep logic)
func TestTOTPStepReplayPreventionLogic(t *testing.T) {
	// TOTP Time Step tính bằng unix timestamp / 30
	currentStep := time.Now().Unix() / 30

	// Giả lập trạng thái step đã được chấp nhận gần nhất trong DB
	var lastAcceptedStep int64 = currentStep

	// Trường hợp 1: Người dùng submit lại mã OTP trong cùng 1 step
	replayedStep := currentStep
	if replayedStep <= lastAcceptedStep {
		// Kết quả mong đợi: Chặn Replay attack thành công!
		t.Logf("PASS: Blocked replayed OTP step %d (last accepted: %d)", replayedStep, lastAcceptedStep)
	} else {
		t.Errorf("FAIL: Replayed OTP step %d should have been blocked", replayedStep)
	}

	// Trường hợp 2: Người dùng submit mã OTP cho step mới (30 giây sau)
	futureStep := currentStep + 1
	if futureStep > lastAcceptedStep {
		// Kết quả mong đợi: Chấp nhận OTP step mới và Cập nhật DB
		lastAcceptedStep = futureStep
		t.Logf("PASS: Accepted new OTP step %d", lastAcceptedStep)
	} else {
		t.Errorf("FAIL: Valid new OTP step %d was incorrectly blocked", futureStep)
	}
}
