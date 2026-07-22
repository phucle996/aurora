package mailRepoImpl

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"controlplane/internal/config"
	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"
	mailTaxonomy "controlplane/internal/mail/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type infrastructureRepoPostgres struct {
	db              *pgxpool.Pool
	mailSchema      string
	hierarchySchema string
}

func NewInfrastructureRepository(db *pgxpool.Pool, cfg *config.Config) mailRepoInterface.InfrastructureRepository {
	return &infrastructureRepoPostgres{
		db:              db,
		mailSchema:      cfg.SchemaSQL.Mail,
		hierarchySchema: cfg.SchemaSQL.Hierarchy,
	}
}

func (r *infrastructureRepoPostgres) GetByZoneID(ctx context.Context, zoneID uuid.UUID) (*mailEntity.MailInfrastructure, error) {
	if zoneID == uuid.Nil {
		return nil, mailTaxonomy.ErrInvalidArgument
	}
	result := &mailEntity.MailInfrastructure{ZoneID: zoneID}
	var eventID string
	var reportGeneration int64
	var reportSequence int64
	var capacity int32
	var pendingItems int64
	var inFlightBatches int64
	var dataplaneJSON string
	var stalwartJSON string
	var reportedAt sql.NullTime
	var expiresAt sql.NullTime

	// [COMMENT]: Một LEFT JOIN trả desired/actual kể cả trước heartbeat đầu tiên; Admin thấy
	// "unknown/no snapshot" thay vì nhầm thành Zone không tồn tại.
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT service.desired_state, service.actual_state::text,
		       COALESCE(report.event_id::text, ''),
		       COALESCE(report.report_generation, 0), COALESCE(report.report_sequence, 0),
		       COALESCE(report.service_state, 'unknown'), COALESCE(report.capacity, 0),
		       COALESCE(report.pending_items, 0), COALESCE(report.in_flight_batches, 0),
		       COALESCE(report.probe_node_id, ''),
		       COALESCE(report.dataplane_nodes, '[]'::jsonb)::text,
		       COALESCE(report.stalwart_nodes, '[]'::jsonb)::text,
		       COALESCE(report.inventory_truncated, false), COALESCE(report.error_code, ''),
		       report.reported_at, report.expires_at
		FROM %s.zone_services AS service
		LEFT JOIN %s.mail_infrastructure_reports AS report ON report.zone_id=service.zone_id
		WHERE service.zone_id=$1 AND service.service_type='mail'
	`, r.hierarchySchema, r.mailSchema), zoneID).Scan(
		&result.DesiredState,
		&result.ActualState,
		&eventID,
		&reportGeneration,
		&reportSequence,
		&result.ServiceState,
		&capacity,
		&pendingItems,
		&inFlightBatches,
		&result.ProbeNodeID,
		&dataplaneJSON,
		&stalwartJSON,
		&result.InventoryTruncated,
		&result.ErrorCode,
		&reportedAt,
		&expiresAt,
	)
	if err == pgx.ErrNoRows {
		return nil, mailTaxonomy.ErrInfrastructureNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("mail infrastructure repo: get by zone: %w", err)
	}
	if eventID != "" {
		parsed, parseErr := uuid.Parse(eventID)
		if parseErr != nil {
			return nil, fmt.Errorf("mail infrastructure repo: invalid projected event id: %w", parseErr)
		}
		result.EventID = &parsed
	}
	if reportGeneration < 0 || reportSequence < 0 || capacity < 0 || pendingItems < 0 || inFlightBatches < 0 {
		return nil, fmt.Errorf("mail infrastructure repo: negative projected numeric field")
	}
	result.ReportGeneration = uint64(reportGeneration)
	result.ReportSequence = uint64(reportSequence)
	result.Capacity = uint32(capacity)
	result.PendingItems = uint64(pendingItems)
	result.InFlightBatches = uint64(inFlightBatches)
	if err = json.Unmarshal([]byte(dataplaneJSON), &result.DataplaneNodes); err != nil {
		return nil, fmt.Errorf("mail infrastructure repo: decode dataplane nodes: %w", err)
	}
	if err = json.Unmarshal([]byte(stalwartJSON), &result.StalwartNodes); err != nil {
		return nil, fmt.Errorf("mail infrastructure repo: decode stalwart nodes: %w", err)
	}
	if reportedAt.Valid {
		value := reportedAt.Time.UTC()
		result.ReportedAt = &value
	}
	if expiresAt.Valid {
		value := expiresAt.Time.UTC()
		result.ExpiresAt = &value
		result.Fresh = value.After(time.Now().UTC())
	}
	return result, nil
}
