package basispoints

import (
	"bytes"
	"os"
	"reflect"
	"testing"
)

func TestPrimerMatchesSuppliedPythonReference(t *testing.T) {
	golden, err := os.ReadFile("testdata/primer-reference.json")
	if err != nil {
		t.Fatal(err)
	}
	got := PrimerBody("gpt-6-astra", "fixture/workspace", 1)
	if !bytes.Equal(got, bytes.TrimSpace(golden)) {
		t.Fatalf("reference mismatch\n%s\n%s", got, golden)
	}
	if bytes.Contains(got, []byte("service_tier")) {
		t.Fatal("tier must be absent")
	}
}

func TestPrimerCatalogAndPlan(t *testing.T) {
	raw := []byte(`{"allowed":true,"model_catalog":{"restricted_models":["restricted"],"models":[{"id":"gpt-6-astra","efforts":[{"value":"low"}]},{"id":"gpt-6.1-sol"},{"id":"restricted"},{"id":"disabled","enabled":false},{"id":"hidden","model_picker_enabled":false},{"id":"unconfigured","policy":{"state":"unconfigured"}},{"id":"gpt-6-astra"}]}}`)
	models, err := PrimerModels(raw)
	if err != nil || !reflect.DeepEqual(models, []string{"gpt-6-astra", "gpt-6.1-sol"}) {
		t.Fatalf("%v %v", models, err)
	}
	if got := PrimerPlan(models, 3); !reflect.DeepEqual(got, []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6-astra"}) {
		t.Fatal(got)
	}
	if len(PrimerPlan(models, 1)) != 2 || PrimerPlan(nil, 40) != nil {
		t.Fatal("coverage guard")
	}
	for _, raw := range []string{`{}`, `{"allowed":false,"models":[{"id":"gpt-6-astra"}]}`, `{"allowed":true,"models":[]}`, `{"allowed":true,"models":[{"id":"gpt-6-astra","efforts":["high"]}]}`} {
		if _, err := PrimerModels([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
