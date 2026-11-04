package monitor

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
)

// RunManualChecks processes durable Telegram requests until shutdown.
func (s *Service) RunManualChecks(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.db.RecoverTelegramChecks(ctx); err != nil && ctx.Err() == nil {
			log.Printf("manual checks recovery: %v", err)
		}
		worked, err := s.runManualCheck(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("manual check: %v", err)
		}
		if worked && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) runManualCheck(ctx context.Context) (bool, error) {
	id, source, attempt, err := s.db.ClaimTelegramCheck(ctx)
	if err != nil || source == 0 {
		return false, err
	}
	problem, err := s.Check(ctx, source)
	if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrNotConfirmed) {
		return true, err
	}
	message := "Ручная проверка завершена."
	if err != nil {
		message = "Ручная проверка не выполнена: источник удалён или выключен."
	} else if problem != "" {
		message = "Ручная проверка: требуется внимание — " + problem
	}
	if err = s.db.QueueNotification(ctx, fmt.Sprintf("telegram-check:%d", id), message); err != nil {
		return true, err
	}
	if err = s.db.FinishTelegramCheck(ctx, id, attempt); err != nil {
		return true, err
	}
	return true, nil
}
