package server

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDesktopBPSImagesUploadReplayAndAccountIsolation(t *testing.T) {
	f := newFixture(t, nil, testAccount("acct_one", "one@example.com"), testAccount("acct_two", "two@example.com"))
	localIDs := make(map[string]string)
	for _, acc := range f.srv.Store.List() {
		localIDs[acc.AccountID] = acc.ID
	}
	imageBytes := []byte("synthetic fixture image bytes")
	var uploads atomic.Int32
	attachments := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uploads.Add(1)
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer file.Close()
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		data, _ := io.ReadAll(file)
		if !bytes.Equal(data, imageBytes) || header.Header.Get("Content-Type") != "image/png" {
			t.Error("image bytes or MIME changed")
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Error("missing attachment auth")
		}
		_, _ = io.WriteString(w, `{"file_id":"file-`+r.Header.Get("Chatgpt-Account-Id")+`"}`)
	}))
	defer attachments.Close()
	f.srv.Router.BPS.AttachmentsURL = attachments.URL
	var input []any
	for i := 0; i < 25; i++ {
		input = append(input, object{"role": "user", "content": []any{object{"type": "input_image", "image_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBytes), "detail": "original"}}})
	}
	body := object{"model": "gpt-6-astra", "input": input, "stream": true, "prompt_cache_key": "desktop-image-replay"}
	for i, accountID := range []string{"acct_one", "acct_one", "acct_two"} {
		resp, result := post(t, f.api.URL+"/v1/responses", body, map[string]string{"X-GPTBridge-Account": localIDs[accountID], "X-GPTBridge-Channel": "bps"})
		if resp.StatusCode != 200 || !strings.Contains(result, "response.completed") {
			t.Fatalf("image request failed: %d %s", resp.StatusCode, result)
		}
		if resp.Header.Get("X-GPTBridge-Upstream") != "basispoints" || f.codexHit.Load() != 0 {
			t.Fatal("channel changed")
		}
		sent := f.bpsBody.Load().(object)
		if sent["model"] != "gpt-6-astra" {
			t.Fatal("model changed")
		}
		count := 0
		for _, item := range sent["input"].([]any) {
			for _, part := range item.(object)["content"].([]any) {
				image := part.(object)
				if image["type"] != "input_image" {
					continue
				}
				count++
				if image["file_id"] != "file-"+accountID || image["detail"] != "original" || image["image_url"] != nil {
					t.Fatal("image detail, reference or account scope changed")
				}
			}
		}
		if count != 25 {
			t.Fatalf("lost images: %d", count)
		}
		wantUploads := int32(1)
		if i == 2 {
			wantUploads = 2
		}
		if uploads.Load() != wantUploads {
			t.Fatalf("uploads=%d want=%d", uploads.Load(), wantUploads)
		}
	}
	input = append(input, object{"role": "user", "content": []any{object{"type": "input_image", "image_url": "https://example.com/i.png", "detail": "invalid"}}})
	body["input"] = input
	resp, result := post(t, f.api.URL+"/v1/responses", body, map[string]string{"X-GPTBridge-Account": localIDs["acct_one"], "X-GPTBridge-Channel": "bps"})
	if resp.StatusCode != 400 || !strings.Contains(result, "image detail") || uploads.Load() != 2 || f.bpsHits.Load() != 3 || f.codexHit.Load() != 0 {
		t.Fatalf("invalid image did not fail before upstream: %d %s", resp.StatusCode, result)
	}
}
