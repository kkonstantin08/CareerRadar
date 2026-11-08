package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/kkonstantin08/CareerRadar/internal/jobs"
	"github.com/kkonstantin08/CareerRadar/internal/store"
)

const listBatchSize = 100
const listScanLimit = 5000

func shortText(s string, limit int) string {
	r := []rune(s)
	if len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return s
}

func displayURL(s string) string {
	if len(s) > 1200 {
		return "Ссылка слишком длинная; откройте запись в кабинете."
	}
	return s
}

func jobBatch(ctx context.Context, data dataStore, cursor int64) ([]store.Job, error) {
	if pager, ok := data.(interface {
		JobsFiltered(context.Context, store.JobFilters) ([]store.Job, error)
	}); ok {
		return pager.JobsFiltered(ctx, store.JobFilters{Cursor: cursor, Limit: listBatchSize})
	}
	all, err := data.Jobs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]store.Job, 0, listBatchSize)
	for _, j := range all {
		if cursor > 0 && j.ID >= cursor {
			continue
		}
		out = append(out, j)
		if len(out) == listBatchSize {
			break
		}
	}
	return out, nil
}

func sourceBatch(ctx context.Context, data dataStore, cursor int64) ([]store.Source, error) {
	if pager, ok := data.(interface {
		SourcesAfter(context.Context, int64, int) ([]store.Source, error)
	}); ok {
		return pager.SourcesAfter(ctx, cursor, listBatchSize)
	}
	all, err := data.Sources(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]store.Source, 0, listBatchSize)
	for _, s := range all {
		if s.ID <= cursor {
			continue
		}
		out = append(out, s)
		if len(out) == listBatchSize {
			break
		}
	}
	return out, nil
}

func (b *Bot) jobsPage(ctx context.Context, data dataStore, cursor int64) (string, error) {
	prefs, err := data.Preferences(ctx)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	next := cursor
	scanned := 0
	more := false
	for scanned < listScanLimit {
		batch, err := jobBatch(ctx, data, next)
		if err != nil {
			return "", err
		}
		if len(batch) == 0 {
			break
		}
		for _, j := range batch {
			match := prefs.Match(jobs.Job{Company: j.Company, Location: j.Location, WorkMode: j.WorkMode, Type: j.Type, Level: j.Level, Direction: j.Direction, Language: j.Language, Hours: j.Hours, Salary: j.Salary, SalaryMin: j.SalaryMin, SalaryMax: j.SalaryMax, Currency: j.Currency})
			if match == jobs.Unsuitable {
				next = j.ID
				scanned++
				continue
			}
			line := fmt.Sprintf("%d · %s · %s\n", j.ID, shortText(j.Title, 120), shortText(j.Company, 100))
			if match == jobs.NeedsClarification {
				line += "Требует уточнения\n"
			}
			if j.Closed {
				line += "Закрыта\n"
			}
			if j.Status != "" {
				line += "Отклик: " + j.Status + "\n"
			}
			line += displayURL(j.URL) + "\n"
			if b.cabinetURL != "" {
				if len(b.cabinetURL) <= 400 {
					line += fmt.Sprintf("В кабинете: %s#job-%d\n", b.cabinetURL, j.ID)
				} else {
					line += "Адрес кабинета слишком длинный.\n"
				}
			}
			if out.Len()+len(line) > 2800 {
				more = true
				break
			}
			out.WriteString(line)
			next = j.ID
			scanned++
		}
		if more || len(batch) < listBatchSize {
			break
		}
		if scanned >= listScanLimit {
			remaining, err := jobBatch(ctx, data, next)
			if err != nil {
				return "", err
			}
			more = len(remaining) > 0
			break
		}
	}
	if out.Len() == 0 && !more {
		return "Вакансий пока нет.", nil
	}
	if out.Len() == 0 {
		out.WriteString("На этой странице подходящих вакансий нет.\n")
	}
	if more {
		fmt.Fprintf(&out, "Список продолжен: /jobs %d", next)
	}
	return out.String(), nil
}

func (b *Bot) sourcesPage(ctx context.Context, data dataStore, cursor int64) (string, error) {
	var out strings.Builder
	next := cursor
	scanned := 0
	more := false
	for scanned < listScanLimit {
		batch, err := sourceBatch(ctx, data, next)
		if err != nil {
			return "", err
		}
		if len(batch) == 0 {
			break
		}
		for _, s := range batch {
			state := "выключен"
			if s.Enabled {
				state = "включён"
			}
			line := fmt.Sprintf("%d · %s · %s\n%s\n", s.ID, shortText(s.Name, 120), state, displayURL(s.URL))
			if s.Problem != "" {
				line += "Требует внимания: " + shortText(s.Problem, 80) + "\n"
			}
			if out.Len()+len(line) > 2800 {
				more = true
				break
			}
			out.WriteString(line)
			next = s.ID
			scanned++
		}
		if more || len(batch) < listBatchSize {
			break
		}
		if scanned >= listScanLimit {
			remaining, err := sourceBatch(ctx, data, next)
			if err != nil {
				return "", err
			}
			more = len(remaining) > 0
			break
		}
	}
	if out.Len() == 0 && !more {
		return "Источников пока нет.", nil
	}
	if more {
		fmt.Fprintf(&out, "Список продолжен: /sources %d", next)
	}
	return out.String(), nil
}
