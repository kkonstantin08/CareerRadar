package store

import (
	"context"
	"encoding/json"
	"time"
)

// Revision contains the facts recorded at a particular observation.
type Revision struct {
	Snapshot  json.RawMessage `json:"snapshot"`
	CreatedAt time.Time       `json:"created_at"`
}

// JobDetails contains the current record, previous revisions, and evidence URLs.
type JobDetails struct {
	Job      Job        `json:"job"`
	History  []Revision `json:"history"`
	Evidence []string   `json:"evidence"`
}

// Details retrieves a job's current facts and retained provenance.
func (s *Store) Details(ctx context.Context, id int64) (JobDetails, error) {
	var d JobDetails
	j := &d.Job
	err := s.db.QueryRowContext(ctx, `SELECT id,COALESCE(source_id,0),url,title,company,description,deadline,status,location,work_mode,offer_type,level,weekly_hours,monthly_salary,salary_currency,closed FROM jobs WHERE id=$1`, id).Scan(&j.ID, &j.SourceID, &j.URL, &j.Title, &j.Company, &j.Description, &j.Deadline, &j.Status, &j.Location, &j.WorkMode, &j.Type, &j.Level, &j.Hours, &j.Salary, &j.Currency, &j.Closed)
	if err != nil {
		return d, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT snapshot,created_at FROM job_history WHERE job_id=$1 ORDER BY id DESC LIMIT 100`, id)
	if err != nil {
		return d, err
	}
	d.History = []Revision{}
	for rows.Next() {
		var x Revision
		if err = rows.Scan(&x.Snapshot, &x.CreatedAt); err != nil {
			rows.Close()
			return d, err
		}
		d.History = append(d.History, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return d, err
	}
	evidence, err := s.db.QueryContext(ctx, `SELECT page_url FROM job_evidence WHERE job_url=$1 ORDER BY page_url`, j.URL)
	if err != nil {
		return d, err
	}
	defer evidence.Close()
	d.Evidence = []string{}
	for evidence.Next() {
		var page string
		if err = evidence.Scan(&page); err != nil {
			return d, err
		}
		d.Evidence = append(d.Evidence, page)
	}
	return d, evidence.Err()
}
