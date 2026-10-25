package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/services"
)

const serviceColumns = `id, state, image, active, last_error, last_operation, updated_at`

// SaveServiceState upserts a service's state record.
func (s *Store) SaveServiceState(ctx context.Context, rec services.Record) error {
	updatedAt := rec.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = s.now()
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO services (id, state, image, active, last_error, last_operation, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			state = excluded.state,
			image = excluded.image,
			active = excluded.active,
			last_error = excluded.last_error,
			last_operation = excluded.last_operation,
			updated_at = excluded.updated_at
	`, string(rec.ID), string(rec.State), rec.Image, boolInt(rec.Active), rec.LastError, rec.LastOperation, updatedAt.Unix())
	if err != nil {
		return fmt.Errorf("save service state %q: %w", rec.ID, err)
	}
	return nil
}

// GetServiceState returns the recorded state for a service ID.
func (s *Store) GetServiceState(ctx context.Context, id services.ID) (services.Record, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+serviceColumns+` FROM services WHERE id = ?`, string(id))
	rec, err := scanServiceRecord(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return services.Record{}, ErrNotFound
		}
		return services.Record{}, fmt.Errorf("get service state %q: %w", id, err)
	}
	return rec, nil
}

// ListServiceStates returns all recorded service states ordered by ID.
func (s *Store) ListServiceStates(ctx context.Context) ([]services.Record, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+serviceColumns+` FROM services ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list service states: %w", err)
	}
	defer rows.Close()

	var records []services.Record
	for rows.Next() {
		rec, err := scanServiceRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("scan service state: %w", err)
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate service states: %w", err)
	}
	return records, nil
}

// DeleteServiceState removes a service record by ID. It returns ErrNotFound if no row was deleted.
func (s *Store) DeleteServiceState(ctx context.Context, id services.ID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM services WHERE id = ?`, string(id))
	if err != nil {
		return fmt.Errorf("delete service state %q: %w", id, err)
	}
	return requireAffected(res, "service", string(id))
}

func scanServiceRecord(scanner rowScanner) (services.Record, error) {
	var (
		id            string
		state         string
		image         string
		active        int
		lastError     string
		lastOperation string
		updatedAt     int64
	)
	err := scanner.Scan(&id, &state, &image, &active, &lastError, &lastOperation, &updatedAt)
	if err != nil {
		return services.Record{}, err
	}
	return services.Record{
		ID:            services.ID(id),
		State:         services.State(state),
		Image:         image,
		Active:        active == 1,
		LastError:     lastError,
		LastOperation: lastOperation,
		UpdatedAt:     time.Unix(updatedAt, 0).UTC(),
	}, nil
}
