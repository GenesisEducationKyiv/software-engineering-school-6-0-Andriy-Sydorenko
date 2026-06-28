package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/orchestrator/domain"
)

type sagaLogRow struct {
	SagaID         string `gorm:"column:saga_id;primaryKey;type:uuid"`
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

func (r *Repository) Create(ctx context.Context, rec *domain.SagaRecord) error {
	payload, err := json.Marshal(rec.Payload)
	if err != nil {
		return fmt.Errorf("marshal saga payload: %w", err)
	}
	row := sagaLogRow{
		SagaID:         rec.SagaID,
		State:          string(rec.State),
		SubscriptionID: rec.SubscriptionID,
		Payload:        payload,
	}
	return r.db.WithContext(ctx).Create(&row).Error
}

func (r *Repository) SetState(ctx context.Context, sagaID string, state domain.State, lastErr string) error {
	return r.db.WithContext(ctx).
		Model(&sagaLogRow{}).
		Where("saga_id = ?", sagaID).
		Updates(map[string]any{
			"state":      string(state),
			"last_error": lastErr,
			"updated_at": gorm.Expr("now()"),
		}).Error
}

// FindUnfinished returns sagas the recovery sweep must resume. Unbounded — fine at
// this scale (partial index); add a LIMIT + cursor if a backlog ever forms.
func (r *Repository) FindUnfinished(ctx context.Context) ([]domain.SagaRecord, error) {
	var rows []sagaLogRow
	err := r.db.WithContext(ctx).
		Where("state NOT IN ?", []string{
			string(domain.StateDone),
			string(domain.StateAborted),
			string(domain.StateCompensated),
		}).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}

	recs := make([]domain.SagaRecord, 0, len(rows))
	for i := range rows {
		var payload domain.SagaPayload
		if err := json.Unmarshal(rows[i].Payload, &payload); err != nil {
			return nil, fmt.Errorf("unmarshal saga %s payload: %w", rows[i].SagaID, err)
		}
		recs = append(recs, domain.SagaRecord{
			SagaID:         rows[i].SagaID,
			State:          domain.State(rows[i].State),
			SubscriptionID: rows[i].SubscriptionID,
			Payload:        payload,
		})
	}
	return recs, nil
}
