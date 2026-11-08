// Package audit writes append-only audit events for state-changing
// operations. Callers pass their own transaction so the audit row
// commits atomically with the mutation it describes.
package audit

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/PithomLabs/a1/internal/apperr"
)

// DBTX is the write surface shared by *pgxpool.Pool and pgx.Tx.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Write records one audit event. before and after marshal to JSONB;
// nil marshals to SQL NULL.
func Write(ctx context.Context, q DBTX, employerID, actor, service, action, entity string, entityID *string, before, after any) error {
	beforeRaw, err := marshalNullable(before)
	if err != nil {
		return apperr.New(apperr.CodeValidation, "audit before-state: %v", err)
	}
	afterRaw, err := marshalNullable(after)
	if err != nil {
		return apperr.New(apperr.CodeValidation, "audit after-state: %v", err)
	}
	_, err = q.Exec(ctx,
		`INSERT INTO audit_event (employer_id, actor, service, action, entity, entity_id, before_state, after_state)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		employerID, actor, service, action, entity, entityID, beforeRaw, afterRaw)
	return apperr.FromDB(err, "write audit event")
}

func marshalNullable(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return string(raw), nil
}
