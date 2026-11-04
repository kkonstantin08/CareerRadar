package monitor

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"
	_ "time/tzdata"
)

func nextRun(now time.Time, loc *time.Location) time.Time {
	local := now.In(loc)
	next := time.Date(local.Year(), local.Month(), local.Day(), 8, 0, 0, 0, loc)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// RunDaily checks enabled sources at 08:00 Moscow time without backfilling missed runs.
func (s *Service) RunDaily(ctx context.Context) {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		log.Printf("monitor schedule unavailable: %v", err)
		return
	}
	for ctx.Err() == nil {
		scheduled := nextRun(time.Now(), loc)
		timer := time.NewTimer(time.Until(scheduled))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err := s.runOnce(ctx, scheduled.Format("2006-01-02")); err != nil {
			log.Printf("monitor: daily check failed: %v", err)
		}
	}
}

func (s *Service) runOnce(ctx context.Context, day string) error {
	sources, err := s.db.Sources(ctx)
	if err != nil {
		return err
	}
	sem := make(chan struct{}, 2)
	var wg sync.WaitGroup
	var problems atomic.Int64
	enabled := 0
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		enabled++
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		}
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			defer func() { <-sem }()
			problem, err := s.Check(ctx, id)
			if err != nil || problem != "" {
				problems.Add(1)
			}
			if err != nil {
				log.Printf("monitor: source %d: %v", id, err)
			}
		}(src.ID)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	found, err := s.db.Jobs(ctx)
	if err != nil {
		return err
	}
	message := fmt.Sprintf("Ежедневная сводка за %s\nИсточников проверено: %d\nТребуют внимания: %d\nВакансий сохранено: %d", day, enabled, problems.Load(), len(found))
	return s.db.QueueNotification(ctx, "daily:"+day, message)
}
