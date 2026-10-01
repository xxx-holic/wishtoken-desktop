package basispoints

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	maxInlineImageBytes = 20 << 20
	// Bound the full replay, including tool screenshots, without imposing the
	// old 20-upload limit on a conversation's accumulated image history.
	maxImagesPerCall      = 1500
	maxInlineRequestBytes = 32 << 20
	maxParallelUploads    = 4
)

// validateImage checks one input_image part. Message images must be HTTPS
// links or gateway attachment ids (data URLs are uploaded beforehand); tool
// output screenshots may stay inline when the caller validated them.
func validateImage(part object, toolOutput bool, toolImages map[string]bool) error {
	raw := text(part["image_url"])
	fileID := text(part["file_id"])
	if IsDataURL(raw) {
		if toolOutput || toolImages[raw] {
			return validateDetail(part)
		}
		return prepareErr(CategoryImageInput, "inline data:image input must be uploaded as an attachment first")
	}
	if raw == "" && fileID != "" {
		if !ValidAttachmentID(fileID) {
			return prepareErr(CategoryImageInput, "input_image file_id is not a Basispoints attachment id")
		}
		return validateDetail(part)
	}
	if raw == "" {
		return prepareErr(CategoryImageInput, "input_image requires an HTTPS image_url or an attachment file_id")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || strings.TrimSpace(raw) != raw {
		return prepareErr(CategoryImageInput, "input_image requires an absolute HTTPS image URL without embedded credentials")
	}
	if fileID != "" {
		return prepareErr(CategoryImageInput, "input_image must carry either image_url or file_id, not both")
	}
	return validateDetail(part)
}

func validateDetail(part object) error {
	if detail, exists := part["detail"]; exists && detail != nil {
		switch text(detail) {
		case "auto", "low", "high", "original":
		default:
			return prepareErr(CategoryImageInput, "image detail must be auto, low, high or original")
		}
	}
	return nil
}

// ValidAttachmentID reports whether id looks like a gateway attachment id.
func ValidAttachmentID(id string) bool {
	if !strings.HasPrefix(id, "file-") || len(id) < 6 || len(id) > 256 {
		return false
	}
	for _, ch := range id[5:] {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9', ch == '-', ch == '_':
		default:
			return false
		}
	}
	return true
}

// Attachment is one decoded inline image awaiting upload.
type Attachment struct {
	MIME   string
	Data   []byte
	Digest [32]byte
}

// Extension returns the file extension for the attachment's media type.
func (a Attachment) Extension() string {
	switch a.MIME {
	case "image/png":
		return "png"
	case "image/jpeg":
		return "jpg"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	}
	return "bin"
}

type imagePart struct {
	part       object
	attachment Attachment
}

// ImagePlan lists the inline images of a request that must be uploaded.
type ImagePlan struct {
	source     object
	raw        []byte
	parts      []imagePart
	toolImages map[string]bool
}

// PlanImages decodes and validates every inline message image in raw. Tool
// output screenshots are validated but stay inline.
func PlanImages(raw []byte) (*ImagePlan, error) {
	var source object
	if err := decode(raw, &source); err != nil || source == nil {
		return nil, prepareErr(CategoryRequestJSON, "invalid request JSON")
	}
	plan := &ImagePlan{source: source, raw: raw}
	input, _ := source["input"].([]any)
	var total, count int
	decoded := make(map[string]Attachment)
	for _, entry := range input {
		item, _ := entry.(object)
		field := ""
		switch text(item["type"]) {
		case "", "message":
			field = "content"
		case "function_call_output", "custom_tool_call_output":
			field = "output"
		default:
			continue
		}
		parts, _ := item[field].([]any)
		for _, value := range parts {
			part, _ := value.(object)
			if text(part["type"]) != "input_image" {
				continue
			}
			count++
			if count > maxImagesPerCall {
				return nil, prepareErr(CategoryImageLimit, "full conversation exceeds the bridge limit of %d images (including tool screenshots)", maxImagesPerCall)
			}
			if err := validateDetail(part); err != nil {
				return nil, err
			}
			rawURL := text(part["image_url"])
			if !IsDataURL(rawURL) {
				if err := validateImage(part, field == "output", nil); err != nil {
					return nil, err
				}
				continue
			}
			if _, exists := part["file_id"]; exists {
				return nil, prepareErr(CategoryImageInput, "input_image requires exactly one image reference")
			}
			// Decode a repeated history image only once. Keep every reference
			// and still charge each occurrence against the request byte budget.
			attachment, exists := decoded[rawURL]
			if !exists {
				var err error
				attachment, err = decodeDataURL(rawURL)
				if err != nil {
					return nil, err
				}
				decoded[rawURL] = attachment
			}
			total += len(attachment.Data)
			if total > maxInlineRequestBytes {
				return nil, prepareErr(CategoryImageLimit, "inline images in the full conversation exceed the bridge's 32 MiB decoded request limit")
			}
			if part["detail"] == nil {
				part["detail"] = "auto"
			}
			if field == "output" {
				if plan.toolImages == nil {
					plan.toolImages = make(map[string]bool)
				}
				plan.toolImages[rawURL] = true
				continue
			}
			plan.parts = append(plan.parts, imagePart{part: part, attachment: attachment})
		}
	}
	return plan, nil
}

// HasUploads reports whether message images need attachment uploads.
func (p *ImagePlan) HasUploads() bool { return p != nil && len(p.parts) > 0 }

// ToolImages returns the validated inline tool screenshots.
func (p *ImagePlan) ToolImages() map[string]bool {
	if p == nil {
		return nil
	}
	return p.toolImages
}

// Uploader uploads one attachment and returns its gateway file id.
type Uploader func(ctx context.Context, attachment Attachment) (string, error)

// Apply uploads pending images (deduplicated through cache) and returns the
// rewritten request body with file ids in place of data URLs.
func (p *ImagePlan) Apply(ctx context.Context, cache *AttachmentCache, scope string, upload Uploader) ([]byte, error) {
	if p == nil {
		return nil, nil
	}
	if len(p.parts) == 0 {
		return p.raw, nil
	}
	type attachmentKey struct {
		mime   string
		digest [32]byte
	}
	indices := make(map[attachmentKey]int)
	var unique []Attachment
	for _, part := range p.parts {
		key := attachmentKey{part.attachment.MIME, part.attachment.Digest}
		if _, exists := indices[key]; !exists {
			indices[key] = len(unique)
			unique = append(unique, part.attachment)
		}
	}
	// Bound parallel uploads to keep long image histories responsive. Do not
	// rewrite any reference until all uploads succeed; a failed plan is retryable.
	uploadCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ids := make([]string, len(unique))
	jobs := make(chan int, len(unique))
	for i := range unique {
		jobs <- i
	}
	close(jobs)
	var wg sync.WaitGroup
	var firstError sync.Once
	var uploadErr error
	for range min(maxParallelUploads, len(unique)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if uploadCtx.Err() != nil {
					return
				}
				id, err := cache.get(uploadCtx, scope, unique[i], upload)
				if err != nil {
					firstError.Do(func() { uploadErr = err; cancel() })
					return
				}
				ids[i] = id
			}
		}()
	}
	wg.Wait()
	if uploadErr != nil {
		return nil, uploadErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, part := range p.parts {
		key := attachmentKey{part.attachment.MIME, part.attachment.Digest}
		delete(part.part, "image_url")
		part.part["file_id"] = ids[indices[key]]
	}
	return json.Marshal(p.source)
}

func decodeDataURL(raw string) (Attachment, error) {
	header, payload, ok := strings.Cut(raw[len("data:"):], ",")
	if !ok || !strings.HasSuffix(strings.ToLower(header), ";base64") {
		return Attachment{}, prepareErr(CategoryImageInput, "inline image requires a base64 image data URL")
	}
	declared, _, err := mime.ParseMediaType(header[:len(header)-len(";base64")])
	if err != nil {
		return Attachment{}, prepareErr(CategoryImageInput, "inline image has an invalid media type")
	}
	switch declared {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	case "image/jpg":
		declared = "image/jpeg"
	default:
		return Attachment{}, prepareErr(CategoryImageInput, "inline images must be PNG, JPEG, GIF or WebP")
	}
	if len(payload) > base64.StdEncoding.EncodedLen(maxInlineImageBytes) {
		return Attachment{}, prepareErr(CategoryImageLimit, "inline image exceeds the bridge's 20 MiB per-image limit")
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(payload), "="))
		if err != nil {
			return Attachment{}, prepareErr(CategoryImageInput, "inline image contains invalid base64 data")
		}
	}
	if len(data) == 0 {
		return Attachment{}, prepareErr(CategoryImageInput, "inline image is empty")
	}
	if len(data) > maxInlineImageBytes {
		return Attachment{}, prepareErr(CategoryImageLimit, "inline image exceeds the bridge's 20 MiB per-image limit")
	}
	return Attachment{MIME: declared, Data: data, Digest: sha256.Sum256(data)}, nil
}

// AttachmentCache remembers uploaded file ids per scope for 30 minutes so a
// conversation replaying the same image does not upload it again.
type AttachmentCache struct {
	mu      sync.Mutex
	entries map[string]attachmentEntry
	flights map[string]*attachmentFlight
	active  int
	Now     func() time.Time
}

type attachmentEntry struct {
	id      string
	expires time.Time
	used    time.Time
}

type attachmentFlight struct {
	done chan struct{}
	id   string
	err  error
}

// ErrAttachmentBusy signals that too many uploads are in flight.
var ErrAttachmentBusy = fmt.Errorf("basispoints attachment upload capacity exhausted")

func (c *AttachmentCache) clock() time.Time {
	if c != nil && c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *AttachmentCache) get(ctx context.Context, scope string, att Attachment, upload Uploader) (string, error) {
	if upload == nil {
		return "", prepareErr(CategoryImageInput, "image upload is not available")
	}
	if c == nil {
		id, err := upload(ctx, att)
		if err != nil {
			return "", err
		}
		if !ValidAttachmentID(id) {
			return "", fmt.Errorf("basispoints returned an invalid attachment id")
		}
		return id, nil
	}
	key := scope + "\x00" + att.MIME + "\x00" + string(att.Digest[:])
	c.mu.Lock()
	now := c.clock()
	for k, entry := range c.entries {
		if !now.Before(entry.expires) {
			delete(c.entries, k)
		}
	}
	if entry, ok := c.entries[key]; ok {
		entry.used = now
		c.entries[key] = entry
		c.mu.Unlock()
		return entry.id, nil
	}
	if flight := c.flights[key]; flight != nil {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-flight.done:
			return flight.id, flight.err
		}
	}
	if c.active >= 32 {
		c.mu.Unlock()
		return "", ErrAttachmentBusy
	}
	c.active++
	flight := &attachmentFlight{done: make(chan struct{})}
	if c.flights == nil {
		c.flights = make(map[string]*attachmentFlight)
	}
	c.flights[key] = flight
	c.mu.Unlock()

	id, err := upload(ctx, att)
	if err == nil && !ValidAttachmentID(id) {
		err = fmt.Errorf("basispoints returned an invalid attachment id")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.active--
	delete(c.flights, key)
	if err == nil {
		if c.entries == nil {
			c.entries = make(map[string]attachmentEntry)
		}
		if len(c.entries) >= 512 {
			var oldest string
			var used time.Time
			for k, entry := range c.entries {
				if used.IsZero() || entry.used.Before(used) {
					oldest, used = k, entry.used
				}
			}
			delete(c.entries, oldest)
		}
		c.entries[key] = attachmentEntry{id: id, expires: now.Add(30 * time.Minute), used: c.clock()}
	}
	flight.id, flight.err = id, err
	close(flight.done)
	return id, err
}
