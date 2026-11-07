package answergroup

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
	"strings"
)

type PostgresStore struct {
	db       *sql.DB
	provider RepresentationProvider
	policy   Policy
}

func NewPostgresStore(db *sql.DB, provider RepresentationProvider, policy Policy) *PostgresStore {
	if provider == nil {
		provider = DeterministicTextProvider{}
	}
	if !policy.valid() {
		policy = DefaultPolicy()
	}
	return &PostgresStore{db: db, provider: provider, policy: policy}
}

func insertOutbox(ctx context.Context, tx *sql.Tx, tenantID, aggregateID, eventType string, payload map[string]any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO event_outbox(tenant_id,aggregate_type,aggregate_id,event_type,payload) VALUES($1::uuid,'answer_group',$2::uuid,$3,$4::jsonb)`, tenantID, aggregateID, eventType, raw)
	return err
}

func validIDs(values ...string) bool {
	for _, value := range values {
		if _, err := uuid.Parse(strings.TrimSpace(value)); err != nil {
			return false
		}
	}
	return true
}
