// Command careerradar serves the personal CareerRadar API.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kkonstantin08/CareerRadar/internal/collector"
	"github.com/kkonstantin08/CareerRadar/internal/monitor"
	"github.com/kkonstantin08/CareerRadar/internal/server"
	"github.com/kkonstantin08/CareerRadar/internal/store"
	"github.com/kkonstantin08/CareerRadar/internal/telegram"
)

type config struct{ databaseURL, username, password, address string }

func configFromEnv() (config, error) {
	c := config{databaseURL: os.Getenv("DATABASE_URL"), username: os.Getenv("APP_USER"), password: os.Getenv("APP_PASSWORD"), address: "127.0.0.1:8080"}
	if c.databaseURL == "" || c.username == "" || c.password == "" {
		return c, errors.New("DATABASE_URL, APP_USER and APP_PASSWORD are required")
	}
	return c, nil
}

func main() {
	cfg, err := configFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := store.Open(ctx, cfg.databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	client := collector.Client()
	checks := monitor.New(db, func(ctx context.Context, u string) ([]collector.Job, error) { return collector.Fetch(ctx, client, u) })
	go checks.RunDaily(ctx)
	if token := os.Getenv("TELEGRAM_BOT_TOKEN"); token != "" {
		bot := telegram.New(db)
		bot.SetCheck(checks.Check)
		go bot.Run(ctx, token)
		go telegram.Deliver(ctx, db, token)
	}
	srv := &http.Server{Addr: cfg.address, Handler: server.Handler(db, cfg.username, cfg.password, checks), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("CareerRadar listening on http://%s", cfg.address)
	if err = srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
