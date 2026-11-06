package main

import "testing"

func TestConfigRequiresCredentials(t *testing.T) {
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("APP_USER", "owner")
	t.Setenv("APP_PASSWORD", "")
	if _, err := configFromEnv(); err == nil {
		t.Fatal("missing password accepted")
	}
	t.Setenv("APP_PASSWORD", "correct horse battery staple")
	cfg, err := configFromEnv()
	if err != nil || cfg.address != "127.0.0.1:8080" {
		t.Fatalf("config: %+v %v", cfg, err)
	}
	t.Setenv("HTTP_ADDR", "127.0.0.1:18080")
	cfg, err = configFromEnv()
	if err != nil || cfg.address != "127.0.0.1:18080" {
		t.Fatalf("custom listener: address=%q err=%v", cfg.address, err)
	}
}
