package basispoints

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

func imageRequest(parts ...any) []byte {
	body, _ := json.Marshal(object{"model": "gpt-6-astra", "input": []any{object{"role": "user", "content": parts}}})
	return body
}

func inlineImage(detail string) object {
	return object{"type": "input_image", "image_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("synthetic-image")), "detail": detail}
}

func TestOriginalImageDetailPreserved(t *testing.T) {
	for _, reference := range []object{
		inlineImage("original"),
		{"type": "input_image", "image_url": "https://example.com/image.png", "detail": "original"},
		{"type": "input_image", "file_id": "file-example", "detail": "original"},
	} {
		plan, err := PlanImages(imageRequest(reference))
		if err != nil {
			t.Fatal(err)
		}
		body, err := plan.Apply(context.Background(), nil, "scope", func(context.Context, Attachment) (string, error) { return "file-uploaded", nil })
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := Prepare(body, Options{Scope: "scope", Replay: NewReplayCache()})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(prepared.Body), `"detail":"original"`) {
			t.Fatal("original detail was lost")
		}
	}
}

func TestImageHistoryOverTwentyRetainsEveryImage(t *testing.T) {
	var parts []any
	for i := 0; i < 25; i++ {
		parts = append(parts, inlineImage("high"))
	}
	plan, err := PlanImages(imageRequest(parts...))
	if err != nil {
		t.Fatal(err)
	}
	uploads := 0
	body, err := plan.Apply(context.Background(), &AttachmentCache{}, "scope", func(context.Context, Attachment) (string, error) { uploads++; return "file-shared", nil })
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := Prepare(body, Options{Scope: "scope", Replay: NewReplayCache()})
	if err != nil {
		t.Fatal(err)
	}
	if uploads != 1 || strings.Count(string(prepared.Body), `"file_id":"file-shared"`) != 25 {
		t.Fatalf("uploads=%d, image history was lost", uploads)
	}
}

func TestImageValidationBeforeAnyUpload(t *testing.T) {
	for _, bad := range []object{
		inlineImage("invalid"),
		{"type": "input_image", "image_url": "https://example.com/i.png", "detail": "invalid"},
		{"type": "input_image", "image_url": "http://example.com/i.png"},
		{"type": "input_image", "file_id": "invalid"},
	} {
		if _, err := PlanImages(imageRequest(inlineImage("auto"), bad)); err == nil {
			t.Fatal("invalid later image would allow an earlier upload")
		}
	}
}

func TestImageUploadDedupIncludesMIME(t *testing.T) {
	a, b := inlineImage("auto"), inlineImage("auto")
	b["image_url"] = strings.Replace(text(b["image_url"]), "image/png", "image/jpeg", 1)
	plan, err := PlanImages(imageRequest(a, b))
	if err != nil {
		t.Fatal(err)
	}
	var uploads atomic.Int32
	_, err = plan.Apply(context.Background(), nil, "scope", func(context.Context, Attachment) (string, error) {
		return fmt.Sprintf("file-%d", uploads.Add(1)), nil
	})
	if err != nil || uploads.Load() != 2 {
		t.Fatalf("uploads=%d err=%v", uploads.Load(), err)
	}
}

func TestImageUploadsBoundedOrderedAndRetryable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		parts := make([]any, 9)
		for i := range parts {
			parts[i] = object{"type": "input_image", "image_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte(fmt.Sprint(i)))}
		}
		plan, err := PlanImages(imageRequest(parts...))
		if err != nil {
			t.Fatal(err)
		}
		var active atomic.Int32
		var started atomic.Int32
		gate := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			_, err := plan.Apply(context.Background(), nil, "scope", func(ctx context.Context, att Attachment) (string, error) {
				current := active.Add(1)
				defer active.Add(-1)
				if current > maxParallelUploads {
					t.Error("upload concurrency exceeded")
				}
				started.Add(1)
				select {
				case <-gate:
				case <-ctx.Done():
					return "", ctx.Err()
				}
				if string(att.Data) == "0" {
					return "", errors.New("synthetic upload failure")
				}
				return "file-" + string(att.Data), nil
			})
			done <- err
		}()
		synctest.Wait()
		if started.Load() != maxParallelUploads {
			t.Fatalf("uploads did not run in parallel: %d", started.Load())
		}
		close(gate)
		if err := <-done; err == nil {
			t.Fatal("failed upload was accepted")
		}
		for _, part := range plan.parts {
			if part.part["image_url"] == nil || part.part["file_id"] != nil {
				t.Fatal("failure partially rewrote history")
			}
		}
		body, err := plan.Apply(context.Background(), nil, "scope", func(_ context.Context, att Attachment) (string, error) { return "file-" + string(att.Data), nil })
		if err != nil {
			t.Fatal(err)
		}
		var decoded object
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatal(err)
		}
		images := decoded["input"].([]any)[0].(object)["content"].([]any)
		for i, image := range images {
			if image.(object)["file_id"] != fmt.Sprintf("file-%d", i) {
				t.Fatal("parallel upload reordered images")
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := plan.Apply(ctx, nil, "scope", func(context.Context, Attachment) (string, error) {
			t.Error("upload ran after cancellation")
			return "file-unexpected", nil
		}); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	})
}

func TestToolScreenshotOriginalAndLimits(t *testing.T) {
	for _, kind := range []string{"function_call_output", "custom_tool_call_output"} {
		call := object{"type": "function_call", "call_id": "shot", "name": "screenshot", "arguments": "{}"}
		if kind == "custom_tool_call_output" {
			call = object{"type": "custom_tool_call", "call_id": "shot", "name": "screenshot", "input": "capture"}
		}
		request := func(n int) []byte {
			parts := make([]any, n)
			for i := range parts {
				parts[i] = inlineImage("original")
			}
			return mustJSON(t, object{"model": "gpt-6-astra", "input": []any{call, object{"type": kind, "call_id": "shot", "output": parts}}})
		}
		plan, err := PlanImages(request(25))
		if err != nil {
			t.Fatal(err)
		}
		if plan.HasUploads() {
			t.Fatal("tool screenshots should remain inline")
		}
		body, _ := plan.Apply(context.Background(), nil, "scope", nil)
		prepared, err := Prepare(body, Options{Scope: "scope", Replay: NewReplayCache(), ToolImages: plan.ToolImages()})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(prepared.Body), `"detail":"original"`) != 25 {
			t.Fatal("tool images or detail lost")
		}
		if _, err := PlanImages(request(maxImagesPerCall + 1)); Category(err) != CategoryImageLimit {
			t.Fatalf("tool image bound missing: %v", err)
		}
	}
}

func TestImageSizeBudgetsRemainBounded(t *testing.T) {
	part := inlineImage("auto")
	part["image_url"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(make([]byte, 1<<20))
	parts := make([]any, 33)
	for i := range parts {
		parts[i] = part
	}
	if _, err := PlanImages(imageRequest(parts...)); Category(err) != CategoryImageLimit {
		t.Fatalf("repeated image byte budget missing: %v", err)
	}
	// EncodedLen rounds up to three-byte blocks; enforce the decoded boundary too.
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(make([]byte, maxInlineImageBytes+1))
	if _, err := decodeDataURL(dataURL); Category(err) != CategoryImageLimit {
		t.Fatalf("per-image byte budget missing: %v", err)
	}
}

func TestImageCountIncludesRemoteReferences(t *testing.T) {
	parts := make([]any, maxImagesPerCall)
	for i := range parts {
		parts[i] = object{"type": "input_image", "image_url": "https://example.com/image.png"}
	}
	if _, err := PlanImages(imageRequest(parts...)); err != nil {
		t.Fatal(err)
	}
	parts = append(parts, parts[0])
	if _, err := PlanImages(imageRequest(parts...)); Category(err) != CategoryImageLimit {
		t.Fatalf("remote image bound missing: %v", err)
	}
}
