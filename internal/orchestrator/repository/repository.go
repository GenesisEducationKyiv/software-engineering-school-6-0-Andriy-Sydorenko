package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/orchestrator"
)

type sagaLogRow struct {
	SagaID         string `gorm:"column:saga_id;primaryKey;type:uuid"`
	Type           string `gorm:"column:type"`
	State          string `gorm:"column:state"`
	SubscriptionID string `gorm:"column:subscription_id;type:uuid"`
	Payload        []byte `gorm:"column:payload;type:jsonb"`
	LastError      string `gorm:"column:last_error"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (sagaLogRow) TableName() string { return "saga_log" }

type Repository struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Create(ctx context.Context, rec *orchestrator.SagaRecord) error {
	payload, err := json.Marshal(rec.Payload)
	if err != nil {
		return fmt.Errorf("marshal saga payload: %w", err)
	}
	row := sagaLogRow{
		SagaID:         rec.SagaID,
		Type:           "subscribe",
		State:          string(rec.State),
		SubscriptionID: rec.SubscriptionID,
		Payload:        payload,
	}
	return r.db.WithContext(ctx).Create(&row).Error
}

func (r *Repository) SetState(ctx context.Context, sagaID string, state orchestrator.State, lastErr string) error {
	return r.db.WithContext(ctx).
		Model(&sagaLogRow{}).
		Where("saga_id = ?", sagaID).
		Updates(map[string]any{
			"state":      string(state),
			"last_error": lastErr,
			"updated_at": gorm.Expr("now()"),
		}).Error
}

// FindUnfinished returns sagas the recovery sweep must resume.
func (r *Repository) FindUnfinished(ctx context.Context) ([]orchestrator.SagaRecord, error) {
	var rows []sagaLogRow
	err := r.db.WithContext(ctx).
		Where("state NOT IN ?", []string{
			string(orchestrator.StateDone),
			string(orchestrator.StateAborted),
			string(orchestrator.StateCompensated),
		}).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}

	recs := make([]orchestrator.SagaRecord, 0, len(rows))
	for i := range rows {
		var payload orchestrator.SagaPayload
		if err := json.Unmarshal(rows[i].Payload, &payload); err != nil {
			return nil, fmt.Errorf("unmarshal saga %s payload: %w", rows[i].SagaID, err)
		}
		recs = append(recs, orchestrator.SagaRecord{
			SagaID:         rows[i].SagaID,
			State:          orchestrator.State(rows[i].State),
			SubscriptionID: rows[i].SubscriptionID,
			Payload:        payload,
		})
	}
	return recs, nil
}
