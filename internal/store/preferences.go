package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/kkonstantin08/CareerRadar/internal/jobs"
)

// Preferences returns saved preferences or the initial portfolio preferences.
func (s *Store) Preferences(ctx context.Context) (jobs.Preferences, error) {
	return readPreferences(ctx, s.db)
}

func readPreferences(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (jobs.Preferences, error) {
	var data []byte
	err := db.QueryRowContext(ctx, `SELECT data FROM preferences WHERE singleton=true`).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return initialPreferences(), nil
	}
	if err != nil {
		return jobs.Preferences{}, err
	}
	var p jobs.Preferences
	err = json.Unmarshal(data, &p)
	return p, err
}

func initialPreferences() jobs.Preferences {
	hours := 30
	return jobs.Preferences{
		Directions: []string{"Backend", "Frontend", "Fullstack", "AI/ML/DS", "Data Engineering", "DevOps", "QA Automation"},
		Languages:  []string{"Go", "Python"},
		Locations:  []string{"Россия", "Казань", "Иннополис"},
		MaxHours:   &hours,
	}
}

// SavePreferences replaces the owner's filters atomically.
func (s *Store) SavePreferences(ctx context.Context, p jobs.Preferences) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO preferences(singleton,data) VALUES(true,$1) ON CONFLICT(singleton) DO UPDATE SET data=EXCLUDED.data`, data)
	return err
}
