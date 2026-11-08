// Package monitor coordinates bounded source checks and persistence.
package monitor

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/kkonstantin08/CareerRadar/internal/collector"
	"github.com/kkonstantin08/CareerRadar/internal/store"
	"golang.org/x/sync/semaphore"
)

// ErrNotFound indicates a missing source.
var ErrNotFound = errors.New("source not found")

// ErrNotConfirmed indicates a source that has not been enabled by its owner.
var ErrNotConfirmed = errors.New("source is not confirmed")

// Service serializes checks of the same source within the application process.
type Service struct {
	db          *store.Store
	fetch       func(context.Context, string) ([]collector.Job, error)
	locks       [64]chan struct{}
	limit       chan struct{}
	habrGate    *semaphore.Weighted
	jobicyGate  chan struct{}
	jobicyBatch func(context.Context, []string) (map[string]string, error)
	jobicyFetch func(context.Context, string, string) ([]collector.Job, error)
}

// SetJobicyBatch configures the shared Jobicy synchronization pass before checks start.
func (s *Service) SetJobicyBatch(batch func(context.Context, []string) (map[string]string, error), fetch func(context.Context, string, string) ([]collector.Job, error)) {
	s.jobicyBatch, s.jobicyFetch = batch, fetch
}

func (s *Service) jobicyStatus(ctx context.Context, id string, sources []store.Source) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	select {
	case s.jobicyGate <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-s.jobicyGate }()
	status, err := s.db.JobicyStatus(ctx, id)
	if err != nil || status != "" {
		return status, err
	}
	allowed, err := s.db.ReserveJobicyPoll(ctx)
	if err != nil || !allowed {
		return "", err
	}
	var ids []string
	seen := map[string]bool{}
	for _, src := range sources {
		jobID := collector.JobicyID(src.URL)
		if src.Enabled && jobID != "" && !seen[jobID] {
			ids = append(ids, jobID)
			seen[jobID] = true
		}
	}
	all := map[string]string{}
	for len(ids) > 0 {
		n := len(ids)
		if n > 100 {
			n = 100
		}
		batch, err := s.jobicyBatch(ctx, ids[:n])
		if err != nil {
			if all[id] != "" {
				return all[id], nil
			}
			return "", err
		}
		if err := s.db.SaveJobicyStatuses(ctx, batch); err != nil {
			return "", err
		}
		for key, value := range batch {
			all[key] = value
		}
		ids = ids[n:]
	}
	return all[id], nil
}

// New creates a monitor backed by the shared database.
func New(db *store.Store, fetch func(context.Context, string) ([]collector.Job, error)) *Service {
	s := &Service{db: db, fetch: fetch, limit: make(chan struct{}, 2), habrGate: semaphore.NewWeighted(2), jobicyGate: make(chan struct{}, 1)}
	for i := range s.locks {
		s.locks[i] = make(chan struct{}, 1)
	}
	return s
}

// LockHabr serializes Habr observations through database updates on this shared service.
func (s *Service) LockHabr(ctx context.Context) (func(), error) {
	// shortcut: serialize updates through one shared Service; use database ordering for multiple service instances.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.habrGate.Acquire(ctx, 2); err != nil {
		return nil, err
	}
	return func() { s.habrGate.Release(2) }, nil
}

// Check reads a confirmed source and records its result without inferring closure.
func (s *Service) Check(ctx context.Context, id int64) (string, error) {
	lock := s.locks[uint64(id)%uint64(len(s.locks))]
	if err := ctx.Err(); err != nil {
		return "", err
	}
	select {
	case lock <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-lock }()
	sources, err := s.db.Sources(ctx)
	if err != nil {
		return "", err
	}
	var src *store.Source
	for i := range sources {
		if sources[i].ID == id {
			src = &sources[i]
			break
		}
	}
	if src == nil {
		return "", ErrNotFound
	}
	if !src.Enabled {
		return "", ErrNotConfirmed
	}
	if sourceURL, err := url.Parse(src.URL); err == nil && strings.EqualFold(sourceURL.Hostname(), "career.habr.com") {
		release, err := s.LockHabr(ctx)
		if err != nil {
			return "", err
		}
		defer release()
	} else {
		// Any source can redirect to Habr, so it must not overlap a discovery update.
		if err := s.habrGate.Acquire(ctx, 1); err != nil {
			return "", err
		}
		defer s.habrGate.Release(1)
	}
	select {
	case s.limit <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-s.limit }()
	jobID := collector.JobicyID(src.URL)
	status := ""
	if jobID != "" && s.jobicyBatch != nil && s.jobicyFetch != nil {
		status, err = s.jobicyStatus(ctx, jobID, sources)
		if err != nil {
			problem := err.Error()
			if recordErr := s.db.RecordCheck(ctx, src.ID, problem); recordErr != nil {
				return "", recordErr
			}
			return problem, nil
		}
		if status == "" {
			problem := "Jobicy permits one synchronization per hour; check deferred"
			if err = s.db.RecordCheck(ctx, src.ID, problem); err != nil {
				return "", err
			}
			return problem, nil
		}
		problem, checked, err := s.db.JobicyChecked(ctx, src.ID, jobID)
		if err != nil {
			return "", err
		}
		if checked {
			return problem, nil
		}
		if status == "active" {
			evidence := "https://jobicy.com/api/jobs/status?ids=" + jobID
			if err = s.db.ReopenJobicy(ctx, src.URL, evidence); err != nil {
				if errors.Is(err, store.ErrDuplicateJobicy) || errors.Is(err, store.ErrAmbiguousJobIdentity) {
					if recordErr := s.db.RecordCheck(ctx, src.ID, err.Error()); recordErr != nil {
						return "", recordErr
					}
					return err.Error(), nil
				}
				return "", err
			}
		}
	} else if collector.IsJobicyURL(src.URL) {
		// shortcut: one job per hourly pass, batch IDs when multiple Jobicy sources are supported.
		allowed, err := s.db.ReserveJobicyPoll(ctx)
		if err != nil {
			return "", err
		}
		if !allowed {
			problem := "Jobicy permits one synchronization per hour; check deferred"
			if err = s.db.RecordCheck(ctx, src.ID, problem); err != nil {
				return "", err
			}
			return problem, nil
		}
	}
	order, err := s.db.NextCheckOrder(ctx)
	if err != nil {
		return "", err
	}
	var found []collector.Job
	var fetchErr error
	if status != "" {
		found, fetchErr = s.jobicyFetch(ctx, src.URL, status)
	} else {
		found, fetchErr = s.fetch(ctx, src.URL)
	}
	problem := ""
	if fetchErr != nil {
		problem = fetchErr.Error()
	}
	for _, job := range found {
		if job.Closed {
			if fetchErr != nil || job.URL != src.URL {
				problem = "Closure evidence could not be verified"
				continue
			}
			ok, err := s.db.ConfirmClosedURL(ctx, job.URL, job.SourceURL)
			if err != nil {
				if errors.Is(err, store.ErrDuplicateJobicy) || errors.Is(err, store.ErrAmbiguousJobIdentity) {
					problem = err.Error()
					continue
				}
				return "", err
			}
			if !ok {
				problem = "Closed vacancy was not previously observed"
			}
			continue
		}
		if problem == "" && (job.Title == "" || job.Company == "" || job.Deadline == "" || job.LocationUncertain) {
			problem = "Vacancy details require clarification"
		}
		if evidence := collector.HabrArchiveEvidenceURL(src.URL); evidence != "" && evidence == collector.HabrArchiveEvidenceURL(job.URL) && job.SourceURL == evidence && fetchErr == nil {
			if err := s.db.ReopenHabr(ctx, job.URL, evidence); err != nil {
				return "", err
			}
		}
		if evidence := collector.GreenhouseClosureEvidenceURL(src.URL); evidence != "" && evidence == collector.GreenhouseClosureEvidenceURL(job.URL) {
			if err := s.db.ReopenGreenhouse(ctx, job.URL, evidence); err != nil {
				if errors.Is(err, store.ErrAmbiguousJobIdentity) {
					problem = err.Error()
					continue
				}
				return "", err
			}
		}
		if feed := collector.LeverFeedURL(src.URL); feed != "" && feed == collector.LeverFeedURL(job.URL) {
			if err := s.db.ReopenLever(ctx, job.URL, feed); err != nil {
				if errors.Is(err, store.ErrAmbiguousJobIdentity) {
					problem = err.Error()
					continue
				}
				return "", err
			}
		}
		jobURL := job.URL
		if jobURL == "" && len(found) == 1 {
			jobURL = src.URL
		}
		u, err := url.Parse(jobURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
			problem = "Job URL is missing or invalid"
			continue
		}
		_, err = s.db.Observe(ctx, store.Observation{SourceID: src.ID, CheckOrder: order, PageURL: job.SourceURL, URL: jobURL, ApplyURL: job.ApplyURL, Title: job.Title, Company: job.Company, Description: job.Description, Deadline: job.Deadline, Location: job.Location, LocationUncertain: job.LocationUncertain, WorkMode: job.WorkMode, Type: job.Type, Level: job.Level, Direction: job.Direction, Language: job.Language, Hours: job.Hours, Salary: job.Salary, SalaryMin: job.SalaryMin, SalaryMax: job.SalaryMax, Currency: job.Currency})
		if err != nil {
			if errors.Is(err, store.ErrDuplicateJobicy) || errors.Is(err, store.ErrAmbiguousJobIdentity) || errors.Is(err, store.ErrStaleObservation) {
				problem = err.Error()
				continue
			}
			return "", err
		}
	}
	if err = s.db.RecordCheck(ctx, src.ID, problem); err != nil {
		return "", err
	}
	return problem, nil
}
