package server

import (
	"testing"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/router"
)

func TestQualitySummaryCountsDowngradesPerAccount(t *testing.T) {
	now := time.Now().UTC()
	summary := qualitySummary([]router.Record{
		{Time: now, Account: "a@example.test", Quality: "model_match", TurnStateLength: 292},
		{Time: now.Add(-time.Minute), Account: "a@example.test", Quality: "model_mismatch", ModelMismatch: true, TurnStateLength: 312, SafetyFasterModel: "gpt-5.6-luna"},
		{Time: now.Add(-2 * time.Minute), Account: "b@example.test", Quality: "model_match"},
	})
	if summary["checked"] != 3 || summary["model_mismatches"] != 1 || summary["last_model_mismatch"] == "" {
		t.Fatalf("summary counts: %+v", summary)
	}
	accounts := summary["accounts"].(object)
	a := accounts["a@example.test"].(object)
	if a["model_mismatches"] != 1 || a["last_turn_state_length"] != 292 || a["last_quality"] != "model_match" {
		t.Fatalf("account summary: %+v", a)
	}
	b := accounts["b@example.test"].(object)
	if b["model_mismatches"] != 0 {
		t.Fatalf("healthy account marked: %+v", b)
	}
}
