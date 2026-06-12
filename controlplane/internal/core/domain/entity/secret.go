package coreEntity

import "time"

type RuntimeSecret struct {
	Secret      []byte
	Fingerprint string
	CreatedAt   time.Time
}

type RuntimeSecrets struct {
	SecretType string
	Active     RuntimeSecret
	Standby    RuntimeSecret
	LoadedAt   time.Time
}

// Zero là một hàm setter để đảm bảo rằng bộ nhớ chứa secrets được xóa sạch ngay lập tức
// để tránh rò rỉ thông tin nhạy cảm (memory leak).
func (s *RuntimeSecrets) Zero() {
	if s == nil {
		return
	}
	if len(s.Active.Secret) > 0 {
		for i := range s.Active.Secret {
			s.Active.Secret[i] = 0
		}
		s.Active.Secret = nil
	}
	if len(s.Standby.Secret) > 0 {
		for i := range s.Standby.Secret {
			s.Standby.Secret[i] = 0
		}
		s.Standby.Secret = nil
	}
}

type CoreSecretRow struct {
	SecretType         string
	ActiveSecret       string
	ActiveFingerprint  string
	ActiveCreatedAt    time.Time
	StandbySecret      string
	StandbyFingerprint string
	StandbyCreatedAt   time.Time
	UpdatedAt          time.Time
}
