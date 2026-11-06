package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/kkonstantin08/CareerRadar/internal/collector"
)

// ErrAmbiguousJobIdentity preserves existing records when verified aliases conflict.
var ErrAmbiguousJobIdentity = errors.New("conflicting vacancy identities; source requires attention")

func storedAliasURL(ctx context.Context, db interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, raw, identity string) (string, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT j.url FROM job_aliases a LEFT JOIN jobs j ON j.id=a.job_id WHERE a.alias_url IN ($1,$2)`, raw, identity)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	found := ""
	for rows.Next() {
		var candidate *string
		if err := rows.Scan(&candidate); err != nil {
			return "", err
		}
		if candidate == nil || found != "" && *candidate != found {
			return "", ErrAmbiguousJobIdentity
		}
		found = *candidate
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if found != "" {
		return found, nil
	}
	return raw, nil
}

func verifyStoredPostingIdentity(ctx context.Context, tx *sql.Tx, id int64, identity string) error {
	if identity == "" {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT alias_url FROM job_aliases WHERE job_id=$1`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var alias string
		if err = rows.Scan(&alias); err != nil {
			return err
		}
		if previous := collector.PostingURL(alias); previous != "" && previous != identity {
			return ErrAmbiguousJobIdentity
		}
	}
	return rows.Err()
}

func backfillJobAliases(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,url,apply_url FROM jobs`)
	if err != nil {
		return err
	}
	aliases := map[string]int64{}
	for rows.Next() {
		var id int64
		var raw, apply string
		if err = rows.Scan(&id, &raw, &apply); err != nil {
			rows.Close()
			return err
		}
		for _, alias := range []string{raw, collector.PostingURL(raw), collector.PostingURL(apply)} {
			if alias == "" {
				continue
			}
			if previous, ok := aliases[alias]; ok && previous != id {
				aliases[alias] = 0
			} else {
				aliases[alias] = id
			}
		}
		// Legacy rows may already contain conflicting provider IDs; neither may authorize a mutation.
		if posting, target := collector.PostingURL(raw), collector.PostingURL(apply); posting != "" && target != "" && posting != target {
			aliases[posting], aliases[target] = 0, 0
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(aliases) == 0 {
		return nil
	}
	urls := make([]string, 0, len(aliases))
	ids := make([]int64, 0, len(aliases))
	for alias, id := range aliases {
		urls = append(urls, alias)
		ids = append(ids, id)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO job_aliases(alias_url,job_id) SELECT alias,NULLIF(job_id,0) FROM unnest($1::text[],$2::bigint[]) AS a(alias,job_id) ON CONFLICT(alias_url) DO UPDATE SET job_id=EXCLUDED.job_id`, urls, ids)
	return err
}
