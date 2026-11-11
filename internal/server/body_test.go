package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDecodeJSONObjectForms(t *testing.T) {
	body, raw, ok := decodeJSONObject([]byte(`{"model":"x"}`))
	if !ok || body["model"] != "x" || string(raw) != `{"model":"x"}` {
		t.Fatalf("plain object: %v %q", body, raw)
	}
	double, _ := json.Marshal(`{"model":"x"}`)
	body, _, ok = decodeJSONObject(double)
	if !ok || body["model"] != "x" {
		t.Fatalf("double-encoded object: %v", body)
	}
	for _, invalid := range [][]byte{[]byte(`[]`), []byte(`"not-json"`), []byte(`{} {}`), []byte(`{} garbage`), []byte(`"{} {}"`), nil} {
		if _, _, ok := decodeJSONObject(invalid); ok {
			t.Fatalf("accepted non-object body %q", invalid)
		}
	}
}

func TestReadJSONBodyEncodings(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte(`{"model":"x"}`)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", &compressed)
	request.Header.Set("Content-Encoding", "gzip")
	recorder := httptest.NewRecorder()
	body, _, ok := readJSONBody(recorder, request)
	if !ok || body["model"] != "x" {
		t.Fatalf("gzip body rejected: %d %s", recorder.Code, recorder.Body.String())
	}

	zstd := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader([]byte{0x28, 0xb5, 0x2f, 0xfd, 0x00, 0x00}))
	recorder = httptest.NewRecorder()
	if _, _, ok := readJSONBody(recorder, zstd); ok || recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("zstd body: ok=%v status=%d", ok, recorder.Code)
	}
}
