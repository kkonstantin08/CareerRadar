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
		if d := r.Details; d != nil && (d.Closed || discovery.VacancyURL(d.URL) != discovery.VacancyURL(r.URL) || discovery.VacancyURL(d.SourceURL) != discovery.VacancyURL(r.URL)) {
			return errors.New("invalid discovery card evidence")
		}
	}
	src, err := s.AddSource(ctx, discovery.SourceName+" — глобальный поиск", discovery.SourceURL, false)
	if err != nil {
		return err
	}
	problem := ""
	for _, r := range results {
		offerType, level := collector.TitleFacts(r.Title)
		x := Observation{SourceID: src.ID, PageURL: r.URL, URL: discovery.VacancyURL(r.URL), Title: r.Title, Company: r.Company, Location: r.Location, Description: r.Description, WorkMode: r.WorkMode, Type: offerType, Level: level, createOnly: true}
		if d := r.Details; d != nil {
			fallback := x
			x = Observation{SourceID: src.ID, PageURL: d.SourceURL, URL: discovery.VacancyURL(r.URL), Title: d.Title, Company: d.Company, Description: d.Description, Deadline: d.Deadline, Location: d.Location, LocationUncertain: d.LocationUncertain, WorkMode: d.WorkMode, Type: d.Type, Level: d.Level, Direction: d.Direction, Language: d.Language, Hours: d.Hours, Salary: d.Salary, SalaryMin: d.SalaryMin, SalaryMax: d.SalaryMax, Currency: d.Currency, ApplyURL: d.ApplyURL}
			x.initialFallback = &fallback
		}
		_, err = s.Observe(ctx, x)
		if err != nil {
			return err
		}
		if r.Problem != "" {
			problem = "Часть карточек поиска требует уточнения. Подтверждённые сведения сохранены."
		}
	}
	return s.RecordCheck(ctx, src.ID, problem)
}
