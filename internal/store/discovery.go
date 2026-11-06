package store

import (
	"context"
	"errors"
	"github.com/kkonstantin08/CareerRadar/internal/collector"
	"github.com/kkonstantin08/CareerRadar/internal/discovery"
)

// SaveDiscovered retains attributed public results without enabling monitoring.
func (s *Store) SaveDiscovered(ctx context.Context, results []discovery.Result) error {
	if len(results) == 0 {
		return nil
	}
	for _, r := range results {
		if discovery.VacancyURL(r.URL) == "" || r.Source != discovery.SourceName || r.Title == "" {
			return errors.New("invalid discovery result")
		}
	}
	src, err := s.AddSource(ctx, discovery.SourceName+" — глобальный поиск", discovery.SourceURL, false)
	if err != nil {
		return err
	}
	for _, r := range results {
		offerType, level := collector.TitleFacts(r.Title)
		_, err = s.Observe(ctx, Observation{SourceID: src.ID, PageURL: r.URL, URL: discovery.VacancyURL(r.URL), Title: r.Title, Company: r.Company, Location: r.Location, Description: r.Description, WorkMode: r.WorkMode, Type: offerType, Level: level})
		if err != nil {
			return err
		}
	}
	return nil
}
