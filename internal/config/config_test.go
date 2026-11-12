package config

import (
	"path/filepath"
	"testing"
)

func TestNormalizeAndAllowlist(t *testing.T) {
	cfg := Default()
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	if !cfg.BPSModelAllowed("GPT-6-Astra") || !cfg.BPSModelAllowed("gpt-6-astra-2026-08-01") || cfg.BPSModelAllowed("gpt-6-astra-turbo") || cfg.BPSModelAllowed("gpt-5.6-luna") {
		t.Fatalf("allowlist behaviour wrong: %v", cfg.BPSModels)
	}
	cfg.BPSModels = []string{"*"}
	if !cfg.BPSModelAllowed("anything") {
		t.Fatal("wildcard should allow all")
	}
	cfg.RoutePolicy = "bogus"
	if err := cfg.Normalize(); err == nil {
		t.Fatal("expected policy error")
	}
	cfg.RoutePolicy = "codex"
	cfg.Listen = "0.0.0.0:8790"
	if err := cfg.Normalize(); err == nil {
		t.Fatal("non-loopback without allow_remote must fail")
	}
	cfg.AllowRemote = true
	if err := cfg.Normalize(); err == nil {
		t.Fatal("allow_remote without api key must fail")
	}
	cfg.APIKey = "k"
	if err := cfg.Normalize(); err != nil || cfg.RoutePolicy != PolicyCodexOnly {
		t.Fatalf("normalize: %v policy=%s", err, cfg.RoutePolicy)
	}
}

func TestLoadSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := Load(path)
	if err != nil || cfg.Listen != "127.0.0.1:8791" {
		t.Fatalf("default load: %v %+v", err, cfg)
	}
	cfg.DefaultModel = "gpt-6-astra"
	cfg.BPSTransport = "websocket"
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	again, err := Load(path)
	if err != nil || again.DefaultModel != "gpt-6-astra" || again.BPSTransport != "websocket" {
		t.Fatalf("reload: %v %+v", err, again)
	}
}

func TestIsLoopback(t *testing.T) {
	for _, ok := range []string{"127.0.0.1:1", "localhost:8790", "[::1]:8790", "127.0.0.1", ""} {
		if !IsLoopback(ok) {
			t.Fatalf("%q should be loopback", ok)
		}
	}
	for _, bad := range []string{":8791", "0.0.0.0:8790", "192.168.1.2:80", "example.com:443"} {
		if IsLoopback(bad) {
			t.Fatalf("%q should not be loopback", bad)
		}
	}
}

func TestExplicitBPSTransport(t *testing.T) {
	cfg := Default()
	if cfg.BPSTransport != "http" {
		t.Fatal("legacy default changed")
	}
	for _, value := range []string{"http", "websocket", ""} {
		cfg.BPSTransport = value
		if err := cfg.Normalize(); err != nil {
			t.Fatal(err)
		}
	}
	cfg.BPSTransport = "auto"
	if cfg.Normalize() == nil {
		t.Fatal("silent transport fallback must not be configurable")
	}
}
