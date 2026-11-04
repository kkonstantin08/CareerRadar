// Package telegram implements the owner's private Telegram interface.
package telegram

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/kkonstantin08/CareerRadar/internal/jobs"
	"github.com/kkonstantin08/CareerRadar/internal/monitor"
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
	reply, _ := b.handle(ctx, m)
	return reply
}

func (b *Bot) handle(ctx context.Context, m Message) (string, error) {
	if m.ChatType != "private" || m.ChatID != m.UserID {
		return "", nil
	}
	parts := strings.Fields(m.Text)
	if len(parts) == 2 && parts[0] == "/start" {
		if err := b.links.LinkTelegram(ctx, parts[1], m.ChatID); err != nil {
			if errors.Is(err, store.ErrInvalidLink) {
				return "Код недействителен, истёк или аккаунт уже привязан.", nil
			}
			return "", err
		}
		return "Telegram подключён.", nil
	}
	owner, err := b.links.TelegramChat(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if owner != m.ChatID {
		return "", nil
	}
	if len(parts) == 0 {
		return "", nil
	}
	if parts[0] == "/help" || parts[0] == "/start" {
		return "Команды: /jobs, /sources, /add URL название, /confirm ID, /disable ID, /edit ID URL название, /delete ID, /check ID, /status ID номер. Статусы: 1 — не откликался, 2 — откликнулся, 3 — тестовое, 4 — собеседование, 5 — оффер, 6 — отказ.", nil
	}
	data, ok := b.links.(dataStore)
	if !ok {
		return "Данные недоступны.", nil
	}
	switch parts[0] {
	case "/jobs":
		list, err := data.Jobs(ctx)
		if err != nil {
			return "", err
		}
		prefs, err := data.Preferences(ctx)
		if err != nil {
			return "", err
		}
		var out strings.Builder
		for _, j := range list {
			match := prefs.Match(jobs.Job{Company: j.Company, Location: j.Location, WorkMode: j.WorkMode, Type: j.Type, Level: j.Level, Direction: j.Direction, Language: j.Language, Hours: j.Hours, Salary: j.Salary, SalaryMin: j.SalaryMin, SalaryMax: j.SalaryMax, Currency: j.Currency})
			if match == jobs.Unsuitable {
				continue
			}
			line := fmt.Sprintf("%d · %s · %s\n", j.ID, j.Title, j.Company)
			if match == jobs.NeedsClarification {
				line += "Требует уточнения\n"
			}
			if j.Closed {
				line += "Закрыта\n"
			}
			if j.Status != "" {
				line += "Отклик: " + j.Status + "\n"
			}
			line += j.URL + "\n"
			if out.Len()+len(line) > 3000 {
				break
			}
			out.WriteString(line)
		}
		if out.Len() == 0 {
			return "Вакансий пока нет.", nil
		}
		return out.String(), nil
	case "/sources":
		list, err := data.Sources(ctx)
		if err != nil {
			return "", err
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
			return "Источников пока нет.", nil
		}
		return out.String(), nil
	case "/add":
		if len(parts) < 3 {
			return "Используйте /add URL название", nil
		}
		u, err := url.Parse(parts[1])
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
			return "Некорректный URL.", nil
		}
		source, err := data.AddSource(ctx, strings.Join(parts[2:], " "), u.String(), false)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Источник %d сохранён. Для включения: /confirm %d", source.ID, source.ID), nil
	case "/confirm":
		if len(parts) != 2 {
			return "Используйте /confirm ID", nil
		}
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return "Некорректный ID.", nil
		}
		list, err := data.Sources(ctx)
		if err != nil {
			return "", err
		}
		for _, s := range list {
			if s.ID == id {
				if err = data.UpdateSource(ctx, id, s.Name, s.URL, true); err != nil {
					return "", err
				}
				return "Источник включён.", nil
			}
		}
		return "Источник не найден.", nil
	case "/disable", "/edit", "/delete":
		if len(parts) < 2 {
			return "Укажите ID источника.", nil
		}
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return "Некорректный ID.", nil
		}
		list, err := data.Sources(ctx)
		if err != nil {
			return "", err
		}
		for _, s := range list {
			if s.ID != id {
				continue
			}
			switch parts[0] {
			case "/disable":
				if err = data.UpdateSource(ctx, id, s.Name, s.URL, false); err != nil {
					return "", err
				}
				return "Источник выключен.", nil
			case "/edit":
				if len(parts) < 4 {
					return "Используйте /edit ID URL название", nil
				}
				u, e := url.Parse(parts[2])
				if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
					return "Некорректный URL.", nil
				}
				if err = data.UpdateSource(ctx, id, strings.Join(parts[3:], " "), u.String(), false); err != nil {
					if errors.Is(err, store.ErrSourceURLConflict) {
						return "Этот адрес уже добавлен как другой источник.", nil
					}
					return "", err
				}
				return "Источник изменён и выключен до повторного подтверждения.", nil
			case "/delete":
				deleter, ok := b.links.(interface {
					DeleteSource(context.Context, int64) error
				})
				if !ok {
					return "", errors.New("source deletion unavailable")
				}
				if err := deleter.DeleteSource(ctx, id); err != nil {
					return "", err
				}
				return "Источник удалён; вакансии сохранены.", nil
			}
		}
		return "Источник не найден.", nil
	case "/check":
		if len(parts) != 2 || b.check == nil {
			return "Используйте /check ID", nil
		}
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return "Некорректный ID.", nil
		}
		problem, err := b.check(ctx, id)
		if errors.Is(err, monitor.ErrNotFound) || errors.Is(err, monitor.ErrNotConfirmed) {
			return "Источник не найден или выключен.", nil
		}
		if err != nil {
			return "", err
		}
		if problem != "" {
			return "Требует внимания: " + problem, nil
		}
		return "Проверка завершена.", nil
	case "/status":
		statuses := []string{"Не откликался", "Откликнулся", "Тестовое", "Собеседование", "Оффер", "Отказ"}
		if len(parts) != 3 {
			return "Используйте /status ID номер", nil
		}
		id, e1 := strconv.ParseInt(parts[1], 10, 64)
		n, e2 := strconv.Atoi(parts[2])
		if e1 != nil || e2 != nil || n < 1 || n > len(statuses) {
			return "Некорректный ID или номер статуса.", nil
		}
		if err := data.SetStatus(ctx, id, statuses[n-1]); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "Вакансия не найдена.", nil
			}
			return "", err
		}
		return "Статус изменён.", nil
	}
	return "Неизвестная команда. Используйте /help.", nil
}

func (b *Bot) terminalReply(ctx context.Context, updateID, chatID int64, reply string) (bool, string, error) {
	if queue, ok := b.links.(interface {
		RecordTelegramReply(context.Context, int64, int64, string) (bool, error)
	}); ok {
		_, err := queue.RecordTelegramReply(ctx, updateID, chatID, reply)
		return true, "", err
	}
	return false, "", nil
}

// atomicStatus commits a status command and its acknowledgement together.
func (b *Bot) atomicStatus(ctx context.Context, updateID int64, m Message) (bool, string, error) {
	storage, ok := b.links.(interface {
		ApplyTelegramStatus(context.Context, int64, int64, int64, string) (bool, error)
	})
	if !ok || m.ChatType != "private" || m.ChatID != m.UserID {
		return false, "", nil
	}
	parts := strings.Fields(m.Text)
	if len(parts) != 3 || parts[0] != "/status" {
		return false, "", nil
	}
	id, e1 := strconv.ParseInt(parts[1], 10, 64)
	n, e2 := strconv.Atoi(parts[2])
	statuses := []string{"Не откликался", "Откликнулся", "Тестовое", "Собеседование", "Оффер", "Отказ"}
	if e1 != nil || e2 != nil || n < 1 || n > len(statuses) {
		return false, "", nil
	}
	owner, err := b.links.TelegramChat(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return true, "", err
	}
	if owner != m.ChatID {
		return false, "", nil
	}
	applied, err := storage.ApplyTelegramStatus(ctx, updateID, m.ChatID, id, statuses[n-1])
	if errors.Is(err, sql.ErrNoRows) {
		return b.terminalReply(ctx, updateID, m.ChatID, "Вакансия не найдена.")
	}
	if err != nil {
		return true, "", err
	}
	if !applied {
		return true, "", nil
	}
	return true, "Статус изменён.", nil
}

// atomicSource commits a source command and its acknowledgement together.
func (b *Bot) atomicSource(ctx context.Context, updateID int64, m Message) (bool, string, error) {
	storage, ok := b.links.(interface {
		ApplyTelegramSource(context.Context, int64, int64, string, int64, string, string) (int64, bool, error)
	})
	if !ok || m.ChatType != "private" || m.ChatID != m.UserID {
		return false, "", nil
	}
	parts := strings.Fields(m.Text)
	if len(parts) == 0 {
		return false, "", nil
	}
	action := strings.TrimPrefix(parts[0], "/")
	var id int64
	var name, sourceURL string
	switch action {
	case "add":
		if len(parts) < 3 {
			return false, "", nil
		}
		sourceURL = parts[1]
		name = strings.Join(parts[2:], " ")
	case "edit":
		if len(parts) < 4 {
			return false, "", nil
		}
		var err error
		id, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return false, "", nil
		}
		sourceURL = parts[2]
		name = strings.Join(parts[3:], " ")
	case "confirm", "disable", "delete":
		if len(parts) != 2 {
			return false, "", nil
		}
		var err error
		id, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return false, "", nil
		}
	default:
		return false, "", nil
	}
	if action == "add" || action == "edit" {
		u, err := url.Parse(sourceURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
			return false, "", nil
		}
	}
	owner, err := b.links.TelegramChat(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return true, "", err
	}
	if owner != m.ChatID {
		return false, "", nil
	}
	id, applied, err := storage.ApplyTelegramSource(ctx, updateID, m.ChatID, action, id, name, sourceURL)
	if errors.Is(err, store.ErrSourceURLConflict) {
		return b.terminalReply(ctx, updateID, m.ChatID, "Этот адрес уже добавлен как другой источник.")
	}
	if errors.Is(err, sql.ErrNoRows) {
		return b.terminalReply(ctx, updateID, m.ChatID, "Источник не найден.")
	}
	if err != nil {
		return true, "", err
	}
	if !applied {
		return true, "", nil
	}
	switch action {
	case "add":
		return true, fmt.Sprintf("Источник %d сохранён. Для включения: /confirm %d", id, id), nil
	case "edit":
		return true, "Источник изменён и выключен до повторного подтверждения.", nil
	case "confirm":
		return true, "Источник включён.", nil
	case "disable":
		return true, "Источник выключен.", nil
	case "delete":
		return true, "Источник удалён; вакансии сохранены.", nil
	}
	return true, "", nil
}

// atomicLink commits owner binding and acknowledgement without persisting the code.
func (b *Bot) atomicLink(ctx context.Context, updateID int64, m Message) (bool, string, error) {
	storage, ok := b.links.(interface {
		ApplyTelegramLink(context.Context, int64, string, int64) (bool, error)
	})
	if !ok || m.ChatType != "private" || m.ChatID != m.UserID {
		return false, "", nil
	}
	parts := strings.Fields(m.Text)
	if len(parts) != 2 || parts[0] != "/start" {
		return false, "", nil
	}
	applied, err := storage.ApplyTelegramLink(ctx, updateID, parts[1], m.ChatID)
	if errors.Is(err, store.ErrInvalidLink) {
		return false, "", nil
	}
	if err != nil {
		return true, "", err
	}
	if !applied {
		return true, "", nil
	}
	return true, "Telegram подключён.", nil
}

// atomicCheck acknowledges a manual check only when it is durably queued.
func (b *Bot) atomicCheck(ctx context.Context, updateID int64, m Message) (bool, string, error) {
	storage, ok := b.links.(interface {
		QueueTelegramCheck(context.Context, int64, int64, int64) (bool, error)
	})
	if !ok || m.ChatType != "private" || m.ChatID != m.UserID {
		return false, "", nil
	}
	parts := strings.Fields(m.Text)
	if len(parts) != 2 || parts[0] != "/check" {
		return false, "", nil
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return false, "", nil
	}
	owner, err := b.links.TelegramChat(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return true, "", err
	}
	if owner != m.ChatID {
		return false, "", nil
	}
	applied, err := storage.QueueTelegramCheck(ctx, updateID, m.ChatID, id)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, store.ErrTelegramSourceDisabled) {
		return b.terminalReply(ctx, updateID, m.ChatID, "Источник не найден или выключен.")
	}
	if err != nil {
		return true, "", err
	}
	if !applied {
		return true, "", nil
	}
	return true, "Проверка поставлена в очередь.", nil
}
