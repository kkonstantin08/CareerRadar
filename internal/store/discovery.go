package store

import (
	"context"
	"errors"
	"github.com/kkonstantin08/CareerRadar/internal/collector"
	"github.com/kkonstantin08/CareerRadar/internal/discovery"
	"net/url"
)

// SaveDiscovered retains attributed public results without enabling monitoring.
func (s *Store) SaveDiscovered(ctx context.Context, results []discovery.Result) error {
	if len(results) == 0 {
		return nil
	}
	for _, r := range results {
		u, err := url.Parse(r.URL)
		if err != nil || u.Scheme != "https" || u.Hostname() != "remotive.com" || u.User != nil || r.Source != "Remotive" || r.Title == "" {
			return errors.New("invalid discovery result")
		}
	}
	src, err := s.AddSource(ctx, "Remotive — глобальный поиск", "https://remotive.com/remote-jobs", false)
	if err != nil {
		return err
	}
	for _, r := range results {
		offerType, level := collector.TitleFacts(r.Title)
		_, err = s.Observe(ctx, Observation{SourceID: src.ID, PageURL: r.URL, URL: r.URL, Title: r.Title, Company: r.Company, Location: r.Location, Description: r.Description, WorkMode: "remote", Type: offerType, Level: level})
		if err != nil {
			return err
		}
	}
	return nil
}
