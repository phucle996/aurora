package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	kafkainfra "controlplane/infra/kafka"
	walletv1 "controlplane/internal/storage/transport/proto"
	"controlplane/pkg/logger"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const (
	walletAdmissionStream = "billing.wallet.admission.changed.v1"
	walletAdmissionGroup  = "controlplane-storage-wallet-admission-v1"
)

var errInvalidWalletAdmission = errors.New("invalid wallet admission event")

// WalletAdmissionProjection consumes only committed Billing outbox events.
// PostgreSQL is the local read model; Redis is transport and replay source.
type WalletAdmissionProjection struct {
	db          *pgxpool.Pool
	rds         *goredis.Client
	kafka       *kafkainfra.Producer
	schema      string
	topicPrefix string
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

func NewWalletAdmissionProjection(db *pgxpool.Pool, rds *goredis.Client, kafka *kafkainfra.Producer, schema, topicPrefix string) (*WalletAdmissionProjection, error) {
	if db == nil || rds == nil || kafka == nil || strings.TrimSpace(schema) == "" || strings.TrimSpace(topicPrefix) == "" {
		return nil, errors.New("storage wallet admission projection requires database, Redis, Kafka and schema")
	}
	return &WalletAdmissionProjection{db: db, rds: rds, kafka: kafka, schema: schema, topicPrefix: strings.TrimSuffix(topicPrefix, ".")}, nil
}

func (p *WalletAdmissionProjection) Start() error {
	if p == nil {
		return errors.New("storage wallet admission projection is nil")
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := p.rds.XGroupCreateMkStream(ctx, walletAdmissionStream, walletAdmissionGroup, "0").Err(); err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		cancel()
		return fmt.Errorf("create wallet admission consumer group: %w", err)
	}
	p.cancel = cancel
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.run(ctx)
	}()
	return nil
}

func (p *WalletAdmissionProjection) Stop() {
	if p == nil {
		return
	}
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
}

func (p *WalletAdmissionProjection) run(ctx context.Context) {
	consumer := fmt.Sprintf("storage-admission-%s", uuid.NewString())
	lastResourceReconcile := time.Now().Add(-30 * time.Second)
	process := func(streamMessages []goredis.XMessage) {
		for _, message := range streamMessages {
			payload, ok := message.Values["payload"].(string)
			if !ok || len(payload) == 0 {
				p.ack(ctx, message.ID)
				continue
			}
			var event walletv1.WalletAdmissionChangedV1
			if err := proto.Unmarshal([]byte(payload), &event); err != nil {
				// Malformed protobuf is terminal poison; acknowledge it so the
				// stream cannot be blocked by one corrupt entry.
				p.ack(ctx, message.ID)
				continue
			}
			if err := p.apply(ctx, &event); err != nil {
				if errors.Is(err, errInvalidWalletAdmission) {
					p.ack(ctx, message.ID)
				}
				// Database/Kafka failures stay pending for retry.
				continue
			}
			p.ack(ctx, message.ID)
		}
	}
	for ctx.Err() == nil {
		claimed, _, claimErr := p.rds.XAutoClaim(ctx, &goredis.XAutoClaimArgs{
			Stream: walletAdmissionStream, Group: walletAdmissionGroup,
			Consumer: consumer, MinIdle: 30 * time.Second, Start: "0-0", Count: 64,
		}).Result()
		if claimErr == nil {
			process(claimed)
		}
		messages, err := p.rds.XReadGroup(ctx, &goredis.XReadGroupArgs{
			Group: walletAdmissionGroup, Consumer: consumer,
			Streams: []string{walletAdmissionStream, "0"}, Count: 64,
		}).Result()
		if err != nil && !errors.Is(err, goredis.Nil) {
			if ctx.Err() == nil {
				time.Sleep(500 * time.Millisecond)
			}
			continue
		}
		hasPending := false
		for _, stream := range messages {
			if len(stream.Messages) > 0 {
				hasPending = true
				break
			}
		}
		if !hasPending {
			messages, err = p.rds.XReadGroup(ctx, &goredis.XReadGroupArgs{
				Group: walletAdmissionGroup, Consumer: consumer,
				Streams: []string{walletAdmissionStream, ">"}, Count: 64,
				Block: 5 * time.Second,
			}).Result()
			if err != nil {
				if errors.Is(err, goredis.Nil) || ctx.Err() != nil {
					continue
				}
				time.Sleep(500 * time.Millisecond)
				continue
			}
		}
		for _, stream := range messages {
			process(stream.Messages)
		}
		// A wallet transition can be projected before a new bucket is created.
		// Reconcile the local bucket tables so that a later resource receives the
		// current owner admission without requiring another wallet transition.
		if time.Since(lastResourceReconcile) >= 30*time.Second {
			if err := p.reconcileResources(ctx); err != nil && ctx.Err() == nil {
				logger.SysWarn("storage.wallet_admission.reconcile", err.Error())
			}
			lastResourceReconcile = time.Now()
		}
	}
}

func (p *WalletAdmissionProjection) ack(ctx context.Context, id string) {
	_, _ = p.rds.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
		pipe.XAck(ctx, walletAdmissionStream, walletAdmissionGroup, id)
		pipe.XDel(ctx, walletAdmissionStream, id)
		return nil
	})
}

func (p *WalletAdmissionProjection) apply(ctx context.Context, event *walletv1.WalletAdmissionChangedV1) error {
	ownerID, err := uuid.Parse(event.OwnerId)
	if err != nil || ownerID == uuid.Nil || event.WalletVersion <= 0 {
		return fmt.Errorf("%w: owner or version", errInvalidWalletAdmission)
	}
	if event.AdmissionMode != "ALLOW" && event.AdmissionMode != "SUSPEND_BILLABLE" {
		return fmt.Errorf("%w: mode", errInvalidWalletAdmission)
	}
	if event.OwnerType != "PERSONAL" && event.OwnerType != "TENANT" {
		return fmt.Errorf("%w: owner type", errInvalidWalletAdmission)
	}
	if event.AdmissionMode == "ALLOW" && event.RestrictionReason != "" {
		return fmt.Errorf("%w: ALLOW carries restriction reason", errInvalidWalletAdmission)
	}
	if event.AdmissionMode == "SUSPEND_BILLABLE" && event.RestrictionReason == "" {
		return fmt.Errorf("%w: suspended admission reason", errInvalidWalletAdmission)
	}
	effectiveAt, err := time.Parse(time.RFC3339Nano, event.EffectiveAt)
	if err != nil {
		return fmt.Errorf("%w: effective_at: %v", errInvalidWalletAdmission, err)
	}
	var validUntil any
	if event.ValidUntil != "" {
		parsed, parseErr := time.Parse(time.RFC3339Nano, event.ValidUntil)
		if parseErr != nil || !parsed.After(effectiveAt) {
			return fmt.Errorf("%w: valid_until", errInvalidWalletAdmission)
		}
		validUntil = parsed
	}
	eventID, err := uuid.Parse(event.EventId)
	if err != nil || eventID == uuid.Nil {
		return fmt.Errorf("%w: event_id", errInvalidWalletAdmission)
	}

	tx, err := p.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ownerTable := p.schema + ".wallet_admission_projection"
	resourceTable := p.schema + ".resource_admission_projection"
	_, err = tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s AS current (owner_id, owner_type, wallet_version, admission_mode, restriction_reason, effective_at, valid_until, source_event_id, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NOW())
		ON CONFLICT (owner_id, owner_type) DO UPDATE SET
			wallet_version=EXCLUDED.wallet_version, admission_mode=EXCLUDED.admission_mode,
			restriction_reason=EXCLUDED.restriction_reason, effective_at=EXCLUDED.effective_at,
			valid_until=EXCLUDED.valid_until, source_event_id=EXCLUDED.source_event_id, updated_at=NOW()
		WHERE EXCLUDED.wallet_version > current.wallet_version`, ownerTable),
		ownerID, event.OwnerType, event.WalletVersion, event.AdmissionMode, nullableReason(event.RestrictionReason), effectiveAt, validUntil, eventID)
	if err != nil {
		return fmt.Errorf("apply wallet admission owner projection: %w", err)
	}
	// A target can arrive from an older fan-out event after a newer wallet
	// transition has already updated the owner row. Read the fenced owner
	// projection inside the same transaction and use that state for every
	// resource target; otherwise a late ALLOW could temporarily resurrect an
	// older admission on a bucket that was not present in the newer event.
	var currentOwner struct {
		walletVersion int64
		admissionMode string
		restriction   *string
		effectiveAt   time.Time
		validUntil    *time.Time
		sourceEventID uuid.UUID
	}
	if err := tx.QueryRow(ctx, fmt.Sprintf(`
		SELECT wallet_version, admission_mode, restriction_reason, effective_at,
		       valid_until, source_event_id
		FROM %s
		WHERE owner_id=$1 AND owner_type=$2`, ownerTable), ownerID, event.OwnerType).Scan(
		&currentOwner.walletVersion, &currentOwner.admissionMode, &currentOwner.restriction,
		&currentOwner.effectiveAt, &currentOwner.validUntil, &currentOwner.sourceEventID,
	); err != nil {
		return fmt.Errorf("read fenced wallet admission owner projection: %w", err)
	}
	for _, target := range event.StorageTargets {
		resourceID, resourceErr := uuid.Parse(target.ResourceId)
		zoneID, zoneErr := uuid.Parse(target.ZoneId)
		if resourceErr != nil || zoneErr != nil || resourceID == uuid.Nil || zoneID == uuid.Nil || target.ResourceName == "" {
			return fmt.Errorf("%w: storage target", errInvalidWalletAdmission)
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`
			INSERT INTO %s AS current (resource_id, resource_name, zone_id, owner_id, owner_type, wallet_version, admission_mode, restriction_reason, effective_at, valid_until, source_event_id, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NOW())
			ON CONFLICT (resource_id, zone_id) DO UPDATE SET
				resource_name=EXCLUDED.resource_name, owner_id=EXCLUDED.owner_id, owner_type=EXCLUDED.owner_type,
				wallet_version=EXCLUDED.wallet_version, admission_mode=EXCLUDED.admission_mode,
				restriction_reason=EXCLUDED.restriction_reason, effective_at=EXCLUDED.effective_at,
				valid_until=EXCLUDED.valid_until, source_event_id=EXCLUDED.source_event_id, updated_at=NOW()
				WHERE EXCLUDED.wallet_version > current.wallet_version`, resourceTable),
			resourceID, target.ResourceName, zoneID, ownerID, event.OwnerType, currentOwner.walletVersion,
			currentOwner.admissionMode, currentOwner.restriction, currentOwner.effectiveAt,
			currentOwner.validUntil, currentOwner.sourceEventID); err != nil {
			return fmt.Errorf("apply storage admission target projection: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	for _, target := range event.StorageTargets {
		zoneID, zoneErr := uuid.Parse(target.ZoneId)
		if zoneErr != nil || zoneID == uuid.Nil {
			return fmt.Errorf("%w: storage target zone", errInvalidWalletAdmission)
		}
		zoneEvent := proto.Clone(event).(*walletv1.WalletAdmissionChangedV1)
		zoneEvent.EventId = currentOwner.sourceEventID.String()
		zoneEvent.WalletVersion = currentOwner.walletVersion
		zoneEvent.AdmissionMode = currentOwner.admissionMode
		zoneEvent.RestrictionReason = ""
		if currentOwner.restriction != nil {
			zoneEvent.RestrictionReason = *currentOwner.restriction
		}
		zoneEvent.EffectiveAt = currentOwner.effectiveAt.UTC().Format(time.RFC3339Nano)
		zoneEvent.ValidUntil = ""
		if currentOwner.validUntil != nil {
			zoneEvent.ValidUntil = currentOwner.validUntil.UTC().Format(time.RFC3339Nano)
		}
		zoneEvent.StorageTargets = []*walletv1.StorageAdmissionTargetV1{target}
		payload, marshalErr := proto.Marshal(zoneEvent)
		if marshalErr != nil {
			return fmt.Errorf("marshal zone wallet admission event: %w", marshalErr)
		}
		topic := fmt.Sprintf("%s.storage.wallet.admission.%s.v1", p.topicPrefix, zoneID.String())
		if publishErr := p.kafka.Publish(ctx, topic, []byte(zoneEvent.EventId+":"+target.ResourceId), payload); publishErr != nil {
			return fmt.Errorf("publish Zone wallet admission event: %w", publishErr)
		}
	}
	return nil
}

type admissionResourceCandidate struct {
	resourceID    uuid.UUID
	resourceName  string
	zoneID        uuid.UUID
	ownerID       uuid.UUID
	ownerType     string
	walletVersion int64
	admissionMode string
	restriction   string
	effectiveAt   time.Time
	validUntil    *time.Time
	sourceEventID uuid.UUID
}

// reconcileResources repairs the ordering gap where a bucket is created after
// the wallet's admission event was already fanned out. The local owner
// projection remains the authority; this only creates the resource read model
// and republishes a scoped event to the owning Zone.
func (p *WalletAdmissionProjection) reconcileResources(ctx context.Context) error {
	ownerTable := p.schema + ".wallet_admission_projection"
	resourceTable := p.schema + ".resource_admission_projection"
	rows, err := p.db.Query(ctx, fmt.Sprintf(`
		SELECT b.id, b.name, b.zone_id, w.owner_id, 'PERSONAL',
		       w.wallet_version, w.admission_mode, COALESCE(w.restriction_reason, ''),
		       w.effective_at, w.valid_until, w.source_event_id
		FROM %s.personal_buckets b
		JOIN hierarchy.personal_workspaces pw ON pw.id = b.workspace_id
		JOIN %s w ON w.owner_id = pw.owner_id AND w.owner_type = 'PERSONAL'
		LEFT JOIN %s r ON r.resource_id = b.id AND r.zone_id = b.zone_id
		WHERE r.resource_id IS NULL OR r.wallet_version < w.wallet_version
		   OR r.owner_id IS DISTINCT FROM w.owner_id OR r.owner_type IS DISTINCT FROM w.owner_type
		UNION ALL
		SELECT b.id, b.name, b.zone_id, w.owner_id, 'TENANT',
		       w.wallet_version, w.admission_mode, COALESCE(w.restriction_reason, ''),
		       w.effective_at, w.valid_until, w.source_event_id
		FROM %s.tenant_buckets b
		JOIN hierarchy.tenant_workspaces tw ON tw.id = b.workspace_id
		JOIN %s w ON w.owner_id = tw.tenant_id AND w.owner_type = 'TENANT'
		LEFT JOIN %s r ON r.resource_id = b.id AND r.zone_id = b.zone_id
		WHERE r.resource_id IS NULL OR r.wallet_version < w.wallet_version
		   OR r.owner_id IS DISTINCT FROM w.owner_id OR r.owner_type IS DISTINCT FROM w.owner_type
	`, p.schema, ownerTable, resourceTable, p.schema, ownerTable, resourceTable))
	if err != nil {
		return fmt.Errorf("list storage admission reconciliation candidates: %w", err)
	}
	defer rows.Close()

	candidates := make([]admissionResourceCandidate, 0)
	for rows.Next() {
		var candidate admissionResourceCandidate
		var validUntil *time.Time
		if err := rows.Scan(
			&candidate.resourceID, &candidate.resourceName, &candidate.zoneID,
			&candidate.ownerID, &candidate.ownerType, &candidate.walletVersion,
			&candidate.admissionMode, &candidate.restriction, &candidate.effectiveAt,
			&validUntil, &candidate.sourceEventID,
		); err != nil {
			return fmt.Errorf("scan storage admission reconciliation candidate: %w", err)
		}
		candidate.validUntil = validUntil
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate storage admission reconciliation candidates: %w", err)
	}
	if len(candidates) == 0 {
		return nil
	}

	tx, err := p.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin storage admission reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	changed := make([]admissionResourceCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		var applied uuid.UUID
		err := tx.QueryRow(ctx, fmt.Sprintf(`
			INSERT INTO %s AS current
				(resource_id, resource_name, zone_id, owner_id, owner_type, wallet_version,
				 admission_mode, restriction_reason, effective_at, valid_until, source_event_id, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NOW())
			ON CONFLICT (resource_id, zone_id) DO UPDATE SET
				resource_name=EXCLUDED.resource_name, owner_id=EXCLUDED.owner_id,
				owner_type=EXCLUDED.owner_type, wallet_version=EXCLUDED.wallet_version,
				admission_mode=EXCLUDED.admission_mode, restriction_reason=EXCLUDED.restriction_reason,
				effective_at=EXCLUDED.effective_at, valid_until=EXCLUDED.valid_until,
				source_event_id=EXCLUDED.source_event_id, updated_at=NOW()
			WHERE EXCLUDED.wallet_version > current.wallet_version
			   OR current.owner_id IS DISTINCT FROM EXCLUDED.owner_id
			RETURNING resource_id`, resourceTable),
			candidate.resourceID, candidate.resourceName, candidate.zoneID, candidate.ownerID,
			candidate.ownerType, candidate.walletVersion, candidate.admissionMode,
			nullableReason(candidate.restriction), candidate.effectiveAt, candidate.validUntil,
			candidate.sourceEventID).Scan(&applied)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("upsert storage admission reconciliation resource: %w", err)
		}
		changed = append(changed, candidate)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit storage admission reconciliation: %w", err)
	}

	for _, candidate := range changed {
		event := &walletv1.WalletAdmissionChangedV1{
			EventId:           candidate.sourceEventID.String(),
			OwnerId:           candidate.ownerID.String(),
			OwnerType:         candidate.ownerType,
			WalletVersion:     candidate.walletVersion,
			AdmissionMode:     candidate.admissionMode,
			RestrictionReason: candidate.restriction,
			EffectiveAt:       candidate.effectiveAt.UTC().Format(time.RFC3339Nano),
			StorageTargets: []*walletv1.StorageAdmissionTargetV1{{
				ResourceId: candidate.resourceID.String(), ResourceName: candidate.resourceName,
				ZoneId: candidate.zoneID.String(),
			}},
		}
		if candidate.validUntil != nil {
			event.ValidUntil = candidate.validUntil.UTC().Format(time.RFC3339Nano)
		}
		payload, err := proto.Marshal(event)
		if err != nil {
			return fmt.Errorf("marshal storage admission reconciliation event: %w", err)
		}
		topic := fmt.Sprintf("%s.storage.wallet.admission.%s.v1", p.topicPrefix, candidate.zoneID.String())
		if err := p.kafka.Publish(ctx, topic, []byte(candidate.sourceEventID.String()+":"+candidate.resourceID.String()), payload); err != nil {
			return fmt.Errorf("publish storage admission reconciliation event: %w", err)
		}
	}
	return nil
}

func nullableReason(reason string) any {
	if reason == "" {
		return nil
	}
	return reason
}
