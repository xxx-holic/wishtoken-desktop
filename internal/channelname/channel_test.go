package channelname

import "testing"

func TestLegacyCompatibilityIsScoped(t *testing.T) {
	old := string([]byte{107, 105, 108, 111})
	for _, value := range []string{old, " " + old + " ", "FENJUE", Fenjue} {
		if Normalize(value) != Fenjue {
			t.Fatal("channel migration")
		}
	}
	if Normalize("codex") != "codex" || Normalize(old+"-other") != old+"-other" {
		t.Fatal("unrelated channel changed")
	}
	if Model("gpt-6-astra-"+old) != "gpt-6-astra-fenjue" || Model("gpt-6-astra") != "gpt-6-astra" {
		t.Fatal("model alias")
	}
	if AdminPath("/"+old+"/login/start") != "/fenjue/login/start" || AdminPath("/accounts") != "/accounts" {
		t.Fatal("admin path alias")
	}
	if IdentityKey(Fenjue) != old || IdentityKey(old) != old || IdentityKey("codex") != "codex" {
		t.Fatal("profile identity changed")
	}
}
