// Package telegram implements the owner's private Telegram interface.
package telegram

import (
	"context"
	"fmt"
	"github.com/kkonstantin08/CareerRadar/internal/jobs"
	"github.com/kkonstantin08/CareerRadar/internal/store"
	"net/url"
	"strconv"
	"strings"
)

type links interface {
	LinkTelegram(context.Context, string, int64) error
	TelegramChat(context.Context) (int64, error)
}

// Message is the subset of a Telegram message used for authorization.
type Message struct {
	ChatID, UserID int64
	ChatType, Text string
}

// Bot handles messages from the linked owner.
type Bot struct {
	links links
	check func(context.Context, int64) (string, error)
}

type dataStore interface {
	Jobs(context.Context) ([]store.Job, error)
	Sources(context.Context) ([]store.Source, error)
	SetStatus(context.Context, int64, string) error
	AddSource(context.Context, string, string, bool) (store.Source, error)
	UpdateSource(context.Context, int64, string, string, bool) error
	Preferences(context.Context) (jobs.Preferences, error)
}

// SetCheck connects manual checks to the shared monitoring service.
func (b *Bot) SetCheck(check func(context.Context, int64) (string, error)) { b.check = check }

// New creates a bot using the shared linking store.
func New(l interface {
	LinkTelegram(context.Context, string, int64) error
	TelegramChat(context.Context) (int64, error)
}) *Bot {
	return &Bot{links: l}
}

// Handle accepts a linking code only in a private chat and ignores other users.
func (b *Bot) Handle(ctx context.Context, m Message) string {
	if m.ChatType != "private" || m.ChatID != m.UserID {
		return ""
	}
	parts := strings.Fields(m.Text)
	if len(parts) == 2 && parts[0] == "/start" {
		if err := b.links.LinkTelegram(ctx, parts[1], m.ChatID); err != nil {
			return "Код недействителен, истёк или аккаунт уже привязан."
		}
		return "Telegram подключён."
	}
	owner, err := b.links.TelegramChat(ctx)
	if err != nil || owner != m.ChatID {
		return ""
	}
	if len(parts) == 0 {
		return ""
	}
	if parts[0] == "/help" || parts[0] == "/start" {
		return "Команды: /jobs, /sources, /add URL название, /confirm ID, /check ID, /status ID номер. Статусы: 1 — не откликался, 2 — откликнулся, 3 — тестовое, 4 — собеседование, 5 — оффер, 6 — отказ."
	}
	data, ok := b.links.(dataStore)
	if !ok {
		return "Данные недоступны."
	}
	switch parts[0] {
	case "/jobs":
		list, err := data.Jobs(ctx)
		if err != nil {
			return "Не удалось загрузить вакансии."
		}
		prefs, err := data.Preferences(ctx)
		if err != nil {
			return "Не удалось загрузить предпочтения."
		}
		var out strings.Builder
		for _, j := range list {
			if prefs.Match(jobs.Job{Company: j.Company, Location: j.Location, WorkMode: j.WorkMode, Type: j.Type, Level: j.Level}) == jobs.Unsuitable {
				continue
			}
			line := fmt.Sprintf("%d · %s · %s\n%s\n", j.ID, j.Title, j.Company, j.URL)
			if out.Len()+len(line) > 3000 {
				break
			}
			out.WriteString(line)
		}
		if out.Len() == 0 {
			return "Вакансий пока нет."
		}
		return out.String()
	case "/sources":
		list, err := data.Sources(ctx)
		if err != nil {
			return "Не удалось загрузить источники."
		}
		var out strings.Builder
		for _, s := range list {
			line := fmt.Sprintf("%d · %s · %s\n", s.ID, s.Name, s.URL)
			if out.Len()+len(line) > 3000 {
				break
			}
			out.WriteString(line)
		}
		if out.Len() == 0 {
			return "Источников пока нет."
		}
		return out.String()
	case "/add":
		if len(parts) < 3 {
			return "Используйте /add URL название"
		}
		u, err := url.Parse(parts[1])
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
			return "Некорректный URL."
		}
		source, err := data.AddSource(ctx, strings.Join(parts[2:], " "), u.String(), false)
		if err != nil {
			return "Не удалось добавить источник."
		}
		return fmt.Sprintf("Источник %d сохранён. Для включения: /confirm %d", source.ID, source.ID)
	case "/confirm":
		if len(parts) != 2 {
			return "Используйте /confirm ID"
		}
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return "Некорректный ID."
		}
		list, err := data.Sources(ctx)
		if err != nil {
			return "Не удалось загрузить источники."
		}
		for _, s := range list {
			if s.ID == id {
				if err = data.UpdateSource(ctx, id, s.Name, s.URL, true); err != nil {
					return "Не удалось подтвердить источник."
				}
				return "Источник включён."
			}
		}
		return "Источник не найден."
	case "/check":
		if len(parts) != 2 || b.check == nil {
			return "Используйте /check ID"
		}
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return "Некорректный ID."
		}
		problem, err := b.check(ctx, id)
		if err != nil {
			return "Не удалось проверить источник."
		}
		if problem != "" {
			return "Требует внимания: " + problem
		}
		return "Проверка завершена."
	case "/status":
		statuses := []string{"Не откликался", "Откликнулся", "Тестовое", "Собеседование", "Оффер", "Отказ"}
		if len(parts) != 3 {
			return "Используйте /status ID номер"
		}
		id, e1 := strconv.ParseInt(parts[1], 10, 64)
		n, e2 := strconv.Atoi(parts[2])
		if e1 != nil || e2 != nil || n < 1 || n > len(statuses) {
			return "Некорректный ID или номер статуса."
		}
		if err := data.SetStatus(ctx, id, statuses[n-1]); err != nil {
			return "Не удалось изменить статус."
		}
		return "Статус изменён."
	}
	return "Неизвестная команда. Используйте /help."
}
