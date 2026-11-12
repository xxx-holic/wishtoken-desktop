package router

import (
	"net/http"
	"testing"
)

func TestModelsEquivalentToleratesSnapshotsButNotSubstitutions(t *testing.T) {
	cases := []struct {
		requested string
		served    string
		want      bool
	}{
		{"gpt-6-astra", "gpt-6-astra", true},
		{"gpt-6-astra", "gpt-6-astra-2026-08-01", true},
		{"gpt-6-astra-1m", "gpt-6-astra", true},
		{"gpt-6-astra", "gpt-5.6-luna", false},
		{"gpt-6-astra", "gpt-6-astra-degrade2-luna-1p-codexswic-ev3", false},
		{"gpt-5.6-luna", "gpt-5.6-luna", true},
	}
	for _, c := range cases {
		if got := modelsEquivalent(c.requested, c.served); got != c.want {
			t.Errorf("modelsEquivalent(%q, %q) = %v, want %v", c.requested, c.served, got, c.want)
		}
	}
}

func TestQualityHeadersAndClassification(t *testing.T) {
	header := http.Header{}
	header.Set("X-Codex-Turn-State", "abcdefghij")
	header.Set("X-Codex-Safety-Buffering-Enabled", "true")
	header.Set("X-Codex-Safety-Buffering-Faster-Model", "gpt-5.6-luna")
	rec := Record{Model: "gpt-6-astra", RequestTurnStateLength: requestTurnStateLength(http.Header{"X-Codex-Turn-State": []string{"client-state"}})}
	noteQualityHeaders(&rec, header)
	if rec.RequestTurnStateLength != len("client-state") || rec.TurnStateLength != 10 || rec.SafetyFasterModel != "gpt-5.6-luna" {
		t.Fatalf("headers not captured: %+v", rec)
	}
	applyQuality(&rec)
	if rec.Quality != "unknown" {
		t.Fatalf("missing response model quality = %q", rec.Quality)
	}
	rec.ResponseModel = "gpt-6-astra"
	applyQuality(&rec)
	if rec.ModelMismatch || rec.Quality != "model_match" {
		t.Fatalf("buffering headers must not claim model substitution: %+v", rec)
	}
	healthy := Record{Model: "gpt-6-astra", ResponseModel: "gpt-6-astra"}
	applyQuality(&healthy)
	if healthy.ModelMismatch || healthy.Quality != "model_match" {
		t.Fatalf("healthy response classified: %+v", healthy)
	}
	healthy.ResponseModel = "gpt-5.6-luna"
	applyQuality(&healthy)
	if !healthy.ModelMismatch || healthy.Quality != "model_mismatch" {
		t.Fatal("substitution not reported")
	}
	healthy.ResponseModel = ""
	applyQuality(&healthy)
	if healthy.ModelMismatch || healthy.Quality != "unknown" {
		t.Fatal("stale classification retained")
	}
}
