// Package server exposes the authenticated personal REST API.
package server

import (
	"context"
	"crypto/subtle"
	"database/sql"
	_ "embed"
	"encoding/csv"
	"encoding/json"
	"errors"
	"github.com/kkonstantin08/CareerRadar/internal/collector"
	"github.com/kkonstantin08/CareerRadar/internal/discovery"
	"github.com/kkonstantin08/CareerRadar/internal/jobs"
	"github.com/kkonstantin08/CareerRadar/internal/monitor"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kkonstantin08/CareerRadar/internal/store"
)

//go:embed index.html
var indexHTML string

//go:embed openapi.json
var openAPI []byte

// New constructs an authenticated handler backed by shared data.
func New(db *store.Store, username, password string) http.Handler {
	client := collector.Client()
	return Handler(db, username, password, monitor.New(db, func(ctx context.Context, u string) ([]collector.Job, error) { return collector.Fetch(ctx, client, u) }))
}

func newHandler(db *store.Store, username, password string, fetch func(context.Context, string) ([]collector.Job, error)) http.Handler {
	return Handler(db, username, password, monitor.New(db, fetch))
}

// Handler exposes the shared monitor and database through HTTP.
func Handler(db *store.Store, username, password string, checks *monitor.Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(openAPI)
	})
	mux.HandleFunc("GET /swagger", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>CareerRadar API</title><link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css"></head><body><div id="swagger-ui"></div><script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script><script>SwaggerUIBundle({url:'/openapi.json',dom_id:'#swagger-ui'})</script></body></html>`))
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(indexHTML))
	})
	mux.HandleFunc("GET /api/discovery/company", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSpace(r.URL.Query().Get("q"))
		if len(name) < 2 || len(name) > 100 {
			http.Error(w, "invalid company name", 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		results, err := discovery.Company(ctx, collector.Client(), name)
		if err != nil {
			http.Error(w, "company discovery unavailable", 502)
			return
		}
		writeJSON(w, 200, results)
	})
	mux.HandleFunc("GET /api/discovery", func(w http.ResponseWriter, r *http.Request) {
		query := strings.TrimSpace(r.URL.Query().Get("q"))
		if len(query) < 2 || len(query) > 100 {
			http.Error(w, "invalid search query", 400)
			return
		}
		results, err := discovery.Search(r.Context(), collector.Client(), query)
		if err != nil {
			http.Error(w, "global search unavailable", 502)
			return
		}
		writeJSON(w, 200, results)
	})
	mux.HandleFunc("GET /api/checks", func(w http.ResponseWriter, r *http.Request) {
		checks, err := db.RecentChecks(r.Context())
		if err != nil {
			http.Error(w, "database error", 500)
			return
		}
		writeJSON(w, 200, checks)
	})
	mux.HandleFunc("GET /api/preferences", func(w http.ResponseWriter, r *http.Request) {
		p, err := db.Preferences(r.Context())
		if err != nil {
			http.Error(w, "database error", 500)
			return
		}
		writeJSON(w, 200, p)
	})
	mux.HandleFunc("PUT /api/preferences", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "expected application/json", 415)
			return
		}
		var p jobs.Preferences
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		dec.DisallowUnknownFields()
		if dec.Decode(&p) != nil || (p.MaxHours != nil && (*p.MaxHours < 0 || *p.MaxHours > 168)) || (p.MinSalary != nil && (*p.MinSalary < 0 || p.Currency == "")) {
			http.Error(w, "invalid preferences", 400)
			return
		}
		if err := db.SavePreferences(r.Context(), p); err != nil {
			http.Error(w, "database error", 500)
			return
		}
		writeJSON(w, 200, p)
	})
	mux.HandleFunc("GET /api/sources", func(w http.ResponseWriter, r *http.Request) {
		xs, err := db.Sources(r.Context())
		if err != nil {
			http.Error(w, "database error", 500)
			return
		}
		writeJSON(w, 200, xs)
	})
	mux.HandleFunc("POST /api/sources", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "expected application/json", http.StatusUnsupportedMediaType)
			return
		}
		var x struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		dec.DisallowUnknownFields()
		if dec.Decode(&x) != nil || strings.TrimSpace(x.Name) == "" || !publicURL(x.URL) {
			http.Error(w, "invalid source", 400)
			return
		}
		source, err := db.AddSource(r.Context(), strings.TrimSpace(x.Name), x.URL, false)
		if err != nil {
			http.Error(w, "database error", 500)
			return
		}
		writeJSON(w, 201, source)
	})
	mux.HandleFunc("POST /api/telegram/link", func(w http.ResponseWriter, r *http.Request) {
		code, err := db.NewLinkCode(r.Context())
		if err != nil {
			http.Error(w, "database error", 500)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, map[string]string{"code": code})
	})
	mux.HandleFunc("POST /api/sources/import", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "text/csv") {
			http.Error(w, "expected text/csv", 415)
			return
		}
		reader := csv.NewReader(http.MaxBytesReader(w, r.Body, 1<<20))
		header, err := reader.Read()
		if err != nil {
			http.Error(w, "invalid CSV header", 400)
			return
		}
		indexes := map[string]int{}
		for i, col := range header {
			indexes[strings.TrimSpace(strings.TrimPrefix(col, "\ufeff"))] = i
		}
		nameIndex, nameOK := indexes["name"]
		urlIndex, urlOK := indexes["url"]
		if !nameOK || !urlOK {
			nameIndex, nameOK = indexes["company"]
			urlIndex, urlOK = indexes["start_url"]
		}
		if !nameOK || !urlOK {
			http.Error(w, "expected name,url or company,start_url columns", 400)
			return
		}
		var sources []store.Source
		for {
			row, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil || len(row) != len(header) || len(sources) >= 1000 {
				http.Error(w, "invalid CSV", 400)
				return
			}
			name, url := strings.TrimSpace(row[nameIndex]), strings.TrimSpace(row[urlIndex])
			if name == "" || !publicURL(url) {
				http.Error(w, "invalid source", 400)
				return
			}
			sources = append(sources, store.Source{Name: name, URL: url})
		}
		if err := db.ImportSources(r.Context(), sources); err != nil {
			http.Error(w, "database error", 500)
			return
		}
		writeJSON(w, 200, map[string]int{"rows": len(sources)})
	})
	mux.HandleFunc("PUT /api/sources/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid ID", 400)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "expected application/json", 415)
			return
		}
		var x struct {
			Name    string `json:"name"`
			URL     string `json:"url"`
			Enabled bool   `json:"enabled"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		dec.DisallowUnknownFields()
		if dec.Decode(&x) != nil || strings.TrimSpace(x.Name) == "" || !publicURL(x.URL) {
			http.Error(w, "invalid source", 400)
			return
		}
		if err = db.UpdateSource(r.Context(), id, strings.TrimSpace(x.Name), x.URL, x.Enabled); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
			} else {
				http.Error(w, "database error", 500)
			}
			return
		}
		writeJSON(w, 200, map[string]bool{"updated": true})
	})
	mux.HandleFunc("DELETE /api/sources/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid ID", 400)
			return
		}
		if err = db.DeleteSource(r.Context(), id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
			} else {
				http.Error(w, "database error", 500)
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/jobs", func(w http.ResponseWriter, r *http.Request) {
		xs, err := db.Jobs(r.Context())
		if err != nil {
			http.Error(w, "database error", 500)
			return
		}
		p, err := db.Preferences(r.Context())
		if err != nil {
			http.Error(w, "database error", 500)
			return
		}
		result := make([]struct {
			store.Job
			Match jobs.Match `json:"match"`
		}, 0, len(xs))
		for _, j := range xs {
			match := p.Match(jobs.Job{Company: j.Company, Location: j.Location, WorkMode: j.WorkMode, Type: j.Type, Level: j.Level, Hours: j.Hours, Salary: j.Salary, Currency: j.Currency})
			if match != jobs.Unsuitable {
				result = append(result, struct {
					store.Job
					Match jobs.Match `json:"match"`
				}{j, match})
			}
		}
		writeJSON(w, 200, result)
	})
	mux.HandleFunc("GET /api/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid ID", 400)
			return
		}
		d, err := db.Details(r.Context(), id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
			} else {
				http.Error(w, "database error", 500)
			}
			return
		}
		writeJSON(w, 200, d)
	})
	mux.HandleFunc("POST /api/jobs/{id}/reopen", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid ID", 400)
			return
		}
		if err = db.Reopen(r.Context(), id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
			} else {
				http.Error(w, "database error", 500)
			}
			return
		}
		writeJSON(w, 200, map[string]bool{"closed": false})
	})
	mux.HandleFunc("POST /api/jobs/{id}/close", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid ID", 400)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "expected application/json", 415)
			return
		}
		var input struct {
			Evidence string `json:"evidence"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || !publicURL(input.Evidence) {
			http.Error(w, "valid evidence URL required", 400)
			return
		}
		if err = db.ConfirmClosed(r.Context(), id, input.Evidence); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
			} else {
				http.Error(w, "database error", 500)
			}
			return
		}
		writeJSON(w, 200, map[string]bool{"closed": true})
	})
	mux.HandleFunc("PUT /api/jobs/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid ID", 400)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "expected application/json", 415)
			return
		}
		var x struct {
			Status string `json:"status"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		dec.DisallowUnknownFields()
		if dec.Decode(&x) != nil {
			http.Error(w, "invalid status", 400)
			return
		}
		switch x.Status {
		case "Не откликался", "Откликнулся", "Тестовое", "Собеседование", "Оффер", "Отказ":
		default:
			http.Error(w, "invalid status", 400)
			return
		}
		if err = db.SetStatus(r.Context(), id, x.Status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
			} else {
				http.Error(w, "database error", 500)
			}
			return
		}
		writeJSON(w, 200, map[string]bool{"updated": true})
	})
	mux.HandleFunc("POST /api/sources/{id}/check", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid ID", 400)
			return
		}
		problem, err := checks.Check(r.Context(), id)
		if err != nil {
			if errors.Is(err, monitor.ErrNotFound) {
				http.NotFound(w, r)
			} else if errors.Is(err, monitor.ErrNotConfirmed) {
				http.Error(w, "source is not confirmed", 409)
			} else {
				http.Error(w, "database error", 500)
			}
			return
		}
		writeJSON(w, 200, map[string]string{"problem": problem})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(user), []byte(username)) != 1 || subtle.ConstantTimeCompare([]byte(pass), []byte(password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="CareerRadar"`)
			http.Error(w, "authentication required", 401)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host {
				http.Error(w, "origin rejected", 403)
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

func publicURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
	}
	return strings.Contains(host, ".")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
