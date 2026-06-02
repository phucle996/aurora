package runtime

import (
	"controlplane/internal/delta-engine/types"
	"encoding/json"
	"fmt"
)

// ApplyMutation ánh xạ và áp dụng một DeltaEvent thô vào RuntimeSnapshot đang được Copy-on-Write.
func ApplyMutation(snap *RuntimeSnapshot, event types.DeltaEvent) error {
	// Đồng bộ phiên bản lớn nhất để theo dõi thứ tự
	if event.Version > snap.Version {
		snap.Version = event.Version
	}

	switch event.Entity {
	case "zone":
		if event.Op == types.OpDelete {
			delete(snap.Zones, event.ID)
			return nil
		}

		var zone types.ZoneState
		if err := json.Unmarshal(event.Payload, &zone); err != nil {
			return fmt.Errorf("runtime: failed to decode zone: %w", err)
		}
		snap.Zones[zone.ID] = zone

	case "rate_policy":
		if event.Op == types.OpDelete {
			delete(snap.RatePolicies, event.ID)
			return nil
		}

		var rp types.RatePolicyState
		if err := json.Unmarshal(event.Payload, &rp); err != nil {
			return fmt.Errorf("runtime: failed to decode rate policy: %w", err)
		}
		snap.RatePolicies[rp.ID] = rp
	}

	return nil
}
