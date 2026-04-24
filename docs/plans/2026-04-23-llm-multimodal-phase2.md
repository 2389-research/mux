# LLM Multimodal Phase 2 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Wire real multimodal translation into Gemini (all four kinds), Ollama (image-only), and OpenRouter (image+PDF+audio); add `ContentTypeVideo` plus `NewVideoFrom*` constructors so Gemini can carry video.

**Architecture:** Extends PR #7's centralized validation + per-provider translation pattern. Gemini uses `genai.NewPartFromBytes` for inline data parts (URL form rejected pre-flight; no auto-fetch in this phase). Ollama and OpenRouter reuse the OpenAI multipart conversion path that PR #7 already wired in — only `Capabilities()` needs to flip.

**Tech Stack:** Go 1.24, `google.golang.org/genai` v1.54.0, `github.com/openai/openai-go` v1.12.0 (used by Ollama + OpenRouter via OpenAI-compatible endpoints).

**Design doc:** `docs/plans/2026-04-23-llm-multimodal-phase2-design.md` — consult if any decision below is unclear.

**Branch:** `feat/llm-multimodal-phase2`, stacked on `feat/llm-multimodal-input` (PR #7). Will auto-retarget to `main` once PR #7 merges.

**Commands:**
- Build: `go build ./...`
- Tests: `go test ./...`
- Targeted: `go test ./llm/... -run <TestName> -v`
- Vet: `go vet ./...`

---

## Task 1: ContentTypeVideo + family validation

**Files:**
- Modify: `llm/types.go`
- Modify: `llm/validate.go`
- Modify: `llm/types_test.go` (append)

**Step 1: Append failing test**

Append to `llm/types_test.go`:

```go
func TestValidateMediaFamily_VideoPrefixMatches(t *testing.T) {
	if err := validateMediaFamily("video", "video/mp4"); err != nil {
		t.Errorf("video/mp4 should pass: %v", err)
	}
	if err := validateMediaFamily("video", "image/png"); err == nil {
		t.Error("image/png should fail video family")
	}
}

func TestValidateRequest_RejectsVideoWhenCapabilityFalse(t *testing.T) {
	caps := Capabilities{Image: true, PDF: true, Audio: true, Video: false}
	req := &Request{
		Messages: []Message{
			{Role: RoleUser, Blocks: []ContentBlock{{
				Type:      ContentTypeVideo,
				MediaType: "video/mp4",
				Source:    &MediaSource{Kind: SourceKindBytes, Bytes: []byte{0, 1, 2}},
			}}},
		},
	}
	err := validateRequest("test", caps, req)
	var unsup *ErrUnsupportedMedia
	if !errors.As(err, &unsup) {
		t.Fatalf("expected *ErrUnsupportedMedia, got %T: %v", err, err)
	}
	if unsup.Media != "video" {
		t.Errorf("Media: %q want video", unsup.Media)
	}
}
```

**Step 2: Run tests to confirm failure**

Run: `go test ./llm/... -run "TestValidateMediaFamily_VideoPrefixMatches|TestValidateRequest_RejectsVideoWhenCapabilityFalse" -v`
Expected: compile error — `ContentTypeVideo` undefined.

**Step 3: Add `ContentTypeVideo` to `llm/types.go`**

Extend the existing const block:

```go
const (
	ContentTypeText       ContentType = "text"
	ContentTypeToolUse    ContentType = "tool_use"
	ContentTypeToolResult ContentType = "tool_result"
	ContentTypeThinking   ContentType = "thinking"
	ContentTypeImage      ContentType = "image"
	ContentTypePDF        ContentType = "pdf"
	ContentTypeAudio      ContentType = "audio"
	ContentTypeVideo      ContentType = "video"
)
```

`validateMediaFamily` already prefix-matches anything that isn't `"pdf"`, so no change needed there.

**Step 4: Add Video case to `checkBlock` in `llm/validate.go`**

```go
case ContentTypeVideo:
	media, supported = "video", caps.Video
```

(Insert in the existing switch alongside Image/PDF/Audio cases.)

**Step 5: Run tests**

Run: `go test ./llm/... -run "TestValidateMediaFamily_VideoPrefixMatches|TestValidateRequest_RejectsVideoWhenCapabilityFalse" -v`
Expected: 2/2 PASS.

Run: `go test ./...` → all packages still pass.

**Step 6: Commit**

```bash
git add llm/types.go llm/validate.go llm/types_test.go
git commit -m "feat(llm): add ContentTypeVideo and capability check"
```

---

## Task 2: Video constructors + tests + fixture

**Files:**
- Modify: `llm/types.go`
- Modify: `llm/types_test.go` (append)
- Create: `llm/testdata/tiny.mp4`

**Step 1: Create fixture**

```bash
# 8-byte synthetic MP4 ftyp box header — enough that the file exists and
# `mime.TypeByExtension(".mp4")` resolves to video/mp4. We never send the
# bytes through to a real model in tests.
printf '\x00\x00\x00\x18ftyp' > llm/testdata/tiny.mp4
```

Verify: `ls -la llm/testdata/tiny.mp4` shows ≥8 bytes.

**Step 2: Append failing tests**

```go
func TestNewVideoFromURL(t *testing.T) {
	b := NewVideoFromURL("https://example.com/v.mp4")
	if b.Type != ContentTypeVideo || b.Source.Kind != SourceKindURL {
		t.Errorf("block: %+v", b)
	}
	if b.MediaType != "" {
		t.Errorf("URL form should not set MediaType, got %q", b.MediaType)
	}
}

func TestNewVideoFromBytes_OK(t *testing.T) {
	b, err := NewVideoFromBytes("video/mp4", []byte{0x00, 0x00, 0x00, 0x18})
	if err != nil {
		t.Fatal(err)
	}
	if b.MediaType != "video/mp4" {
		t.Errorf("MediaType: %q", b.MediaType)
	}
}

func TestNewVideoFromBytes_RejectsNonVideo(t *testing.T) {
	_, err := NewVideoFromBytes("image/png", []byte{0x01})
	if err == nil {
		t.Fatal("expected error for non-video media type")
	}
	if !strings.Contains(err.Error(), "video/") {
		t.Errorf("error should mention video/, got %q", err.Error())
	}
}

func TestNewVideoFromBytes_RejectsEmpty(t *testing.T) {
	if _, err := NewVideoFromBytes("video/mp4", nil); err == nil {
		t.Fatal("expected error for empty bytes")
	}
}

func TestNewVideoFromFile_OK(t *testing.T) {
	b, err := NewVideoFromFile(filepath.Join("testdata", "tiny.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(b.MediaType, "video/") {
		t.Errorf("MediaType: %q", b.MediaType)
	}
	if b.Source.Path != "tiny.mp4" {
		t.Errorf("Path should be basename, got %q", b.Source.Path)
	}
}

func TestNewVideoFromFile_WrongExtension(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "foo.png")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := NewVideoFromFile(p)
	if err == nil {
		t.Fatal("expected error for non-video extension")
	}
}
```

Also extend the existing `TestValidateRequest_RejectsMalformedSourceVariants` table by adding one Video sub-case:

```go
{
	name:   "video empty bytes",
	block:  ContentBlock{Type: ContentTypeVideo, Source: &MediaSource{Kind: SourceKindBytes, Bytes: nil}},
	reason: "empty data",
},
```

**Step 3: Run — confirm fails**

Run: `go test ./llm/... -run TestNewVideo -v`
Expected: compile error — constructors undefined.

**Step 4: Implement constructors in `llm/types.go`**

Add alongside `NewAudioFromURL/Bytes/File`:

```go
// NewVideoFromURL constructs a video content block backed by a remote URL.
// MediaType is left empty; provider translators will reject URL form unless
// they support remote video URLs (Gemini, for instance, requires inline bytes
// today and will return *ErrUnsupportedSource).
func NewVideoFromURL(url string) ContentBlock {
	return ContentBlock{
		Type:   ContentTypeVideo,
		Source: &MediaSource{Kind: SourceKindURL, URL: url},
	}
}

// NewVideoFromBytes constructs a video content block from inline bytes.
// mediaType must be a video/* MIME type; data must be non-empty.
func NewVideoFromBytes(mediaType string, data []byte) (ContentBlock, error) {
	if err := validateMediaFamily("video", mediaType); err != nil {
		return ContentBlock{}, err
	}
	if len(data) == 0 {
		return ContentBlock{}, fmt.Errorf("NewVideoFromBytes: data is empty")
	}
	return ContentBlock{
		Type:      ContentTypeVideo,
		MediaType: mediaType,
		Source:    &MediaSource{Kind: SourceKindBytes, Bytes: data},
	}, nil
}

// NewVideoFromFile reads a video file (extension must map to a video/* MIME
// type), infers its media type from the extension, and returns a
// ready-to-send content block.
func NewVideoFromFile(path string) (ContentBlock, error) {
	data, mediaType, err := readMediaFile(path, "video")
	if err != nil {
		return ContentBlock{}, err
	}
	return ContentBlock{
		Type:      ContentTypeVideo,
		MediaType: mediaType,
		Source:    &MediaSource{Kind: SourceKindFile, Bytes: data, Path: filepath.Base(path)},
	}, nil
}
```

**Step 5: Run tests**

Run: `go test ./llm/... -run "TestNewVideo|TestValidateRequest_RejectsMalformedSourceVariants" -v`
Expected: all PASS (6 new + 5 sub-cases on the table test).

Run: `go test ./...` → all packages pass.

**Step 6: Commit**

```bash
git add llm/types.go llm/types_test.go llm/testdata/tiny.mp4
git commit -m "feat(llm): add video content-block constructors"
```

---

## Task 3: Gemini media translation + URL-form rejection + Capabilities flip

**Files:**
- Modify: `llm/gemini.go`
- Modify: `llm/gemini_test.go`

**Step 1: Append failing tests**

Add to `llm/gemini_test.go`:

```go
func TestGeminiCapabilities(t *testing.T) {
	c, err := NewGeminiClient(context.Background(), "fake-key", "")
	if err != nil {
		t.Fatal(err)
	}
	caps := c.Capabilities()
	if !caps.Image || !caps.PDF || !caps.Audio || !caps.Video {
		t.Errorf("expected all true, got %+v", caps)
	}
}

func TestGeminiConvertRequest_ImageBytes(t *testing.T) {
	img, err := NewImageFromBytes("image/png", []byte{0x89, 'P', 'N', 'G'})
	if err != nil {
		t.Fatal(err)
	}
	contents, _ := convertGeminiRequest(&Request{
		Messages: []Message{NewUserMessageWithBlocks(img)},
	})
	if len(contents) != 1 || len(contents[0].Parts) != 1 {
		t.Fatalf("expected 1 content with 1 part, got %+v", contents)
	}
	part := contents[0].Parts[0]
	if part.InlineData == nil {
		t.Fatalf("expected InlineData, got %+v", part)
	}
	if part.InlineData.MIMEType != "image/png" {
		t.Errorf("MIMEType: %q", part.InlineData.MIMEType)
	}
	if len(part.InlineData.Data) == 0 {
		t.Error("Data should be non-empty")
	}
}

func TestGeminiConvertRequest_PDFBytes(t *testing.T) {
	pdf, err := NewPDFFromBytes([]byte("%PDF-1.4"))
	if err != nil {
		t.Fatal(err)
	}
	contents, _ := convertGeminiRequest(&Request{
		Messages: []Message{NewUserMessageWithBlocks(pdf)},
	})
	part := contents[0].Parts[0]
	if part.InlineData == nil || part.InlineData.MIMEType != "application/pdf" {
		t.Errorf("expected application/pdf inline data, got %+v", part)
	}
}

func TestGeminiConvertRequest_AudioBytes(t *testing.T) {
	audio, err := NewAudioFromBytes("audio/mpeg", []byte{0xff, 0xfb})
	if err != nil {
		t.Fatal(err)
	}
	contents, _ := convertGeminiRequest(&Request{
		Messages: []Message{NewUserMessageWithBlocks(audio)},
	})
	part := contents[0].Parts[0]
	if part.InlineData == nil || part.InlineData.MIMEType != "audio/mpeg" {
		t.Errorf("expected audio/mpeg inline data, got %+v", part)
	}
}

func TestGeminiConvertRequest_VideoBytes(t *testing.T) {
	video, err := NewVideoFromBytes("video/mp4", []byte{0x00, 0x00, 0x00, 0x18})
	if err != nil {
		t.Fatal(err)
	}
	contents, _ := convertGeminiRequest(&Request{
		Messages: []Message{NewUserMessageWithBlocks(video)},
	})
	part := contents[0].Parts[0]
	if part.InlineData == nil || part.InlineData.MIMEType != "video/mp4" {
		t.Errorf("expected video/mp4 inline data, got %+v", part)
	}
}

func TestGeminiCreateMessage_ImageURL_ErrUnsupportedSource(t *testing.T) {
	ctx := context.Background()
	c, err := NewGeminiClient(ctx, "fake-key", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CreateMessage(ctx, &Request{
		Messages: []Message{NewUserMessageWithBlocks(NewImageFromURL("https://example.com/x.png"))},
	})
	var ue *ErrUnsupportedSource
	if !errors.As(err, &ue) {
		t.Fatalf("expected *ErrUnsupportedSource, got %T: %v", err, err)
	}
	if ue.Provider != "gemini" || ue.Media != "image" || ue.Kind != "url" {
		t.Errorf("err fields: %+v", ue)
	}
}

func TestGeminiCreateMessage_VideoURL_ErrUnsupportedSource(t *testing.T) {
	ctx := context.Background()
	c, err := NewGeminiClient(ctx, "fake-key", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CreateMessage(ctx, &Request{
		Messages: []Message{NewUserMessageWithBlocks(NewVideoFromURL("https://example.com/v.mp4"))},
	})
	var ue *ErrUnsupportedSource
	if !errors.As(err, &ue) {
		t.Fatalf("expected *ErrUnsupportedSource, got %T: %v", err, err)
	}
	if ue.Media != "video" || ue.Kind != "url" {
		t.Errorf("err fields: %+v", ue)
	}
}
```

**Delete the now-stale test from PR #7**: remove `TestGeminiCreateMessage_RejectsImageMedia` entirely. It asserted that Gemini's empty `Capabilities{}` rejected image — that assertion is no longer true once we flip Capabilities.

**Step 2: Run — confirm fails**

Run: `go test ./llm/... -run TestGemini -v`
Expected: new tests fail (compile error or assertion mismatch); existing Gemini tests still pass.

**Step 3: Implement helpers + flip capabilities in `llm/gemini.go`**

Replace the existing `Capabilities()` with:

```go
// Capabilities reports which media types Gemini supports as input.
// All four media kinds are translated to inline_data parts with the block's
// MediaType. URL-form sources are rejected pre-flight by validateGeminiSources
// because Gemini's inline_data takes raw bytes and we don't auto-fetch URLs.
func (g *GeminiClient) Capabilities() Capabilities {
	return Capabilities{Image: true, PDF: true, Audio: true, Video: true}
}
```

Extend the per-block switch in `convertMessage` (the existing function near `llm/gemini.go:142`):

```go
case ContentTypeImage, ContentTypePDF, ContentTypeAudio, ContentTypeVideo:
	if part := convertGeminiMedia(block); part != nil {
		parts = append(parts, part)
	}
```

Add helpers at the bottom of the file (above `var _ Client = ...`):

```go
// convertGeminiMedia translates a media block into a Gemini inline_data part.
// URL-form sources are rejected pre-flight by validateGeminiSources, so we
// only see Bytes/File here. Returns nil for malformed blocks (also rejected
// pre-flight) as a defensive fallback.
func convertGeminiMedia(block ContentBlock) *genai.Part {
	if block.Source == nil {
		return nil
	}
	return genai.NewPartFromBytes(block.Source.Bytes, block.MediaType)
}

// validateGeminiSources rejects URL-form media because Gemini's inline_data
// takes raw bytes and we don't auto-fetch URLs.
func validateGeminiSources(req *Request) error {
	for _, msg := range req.Messages {
		for _, block := range msg.Blocks {
			if block.Source == nil || block.Source.Kind != SourceKindURL {
				continue
			}
			switch block.Type {
			case ContentTypeImage, ContentTypePDF, ContentTypeAudio, ContentTypeVideo:
				return &ErrUnsupportedSource{Provider: "gemini", Media: string(block.Type), Kind: "url"}
			}
		}
	}
	return nil
}
```

Wire `validateGeminiSources(req)` into `CreateMessage` and `CreateMessageStream` immediately after the existing `validateRequest("gemini", ...)` call:

```go
if err := validateRequest("gemini", g.Capabilities(), req); err != nil {
	return nil, err
}
if err := validateGeminiSources(req); err != nil {
	return nil, err
}
```

**Step 4: Run tests**

Run: `go test ./llm/... -run TestGemini -v` → all PASS (including pre-existing Gemini tests + 7 new).
Run: `go test ./...` → all packages pass.

**Step 5: Commit**

```bash
git add llm/gemini.go llm/gemini_test.go
git commit -m "feat(llm): Gemini multimodal translation (image/PDF/audio/video)"
```

---

## Task 4: Gemini wire-format JSON snapshot tests

**Files:**
- Modify: `llm/gemini_test.go`

**Step 1: Append snapshot tests**

```go
func TestGeminiWireFormat_ImageBytes(t *testing.T) {
	img, err := NewImageFromBytes("image/png", []byte{0x89, 'P', 'N', 'G'})
	if err != nil {
		t.Fatal(err)
	}
	contents, _ := convertGeminiRequest(&Request{
		Messages: []Message{NewUserMessageWithBlocks(img)},
	})
	body, err := json.Marshal(contents[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if v := gjson.GetBytes(body, "parts.0.inlineData.mimeType").String(); v != "image/png" {
		t.Errorf("mimeType: got %q want image/png; body=%s", v, body)
	}
	if v := gjson.GetBytes(body, "parts.0.inlineData.data").String(); v == "" {
		t.Errorf("data should be non-empty base64; body=%s", body)
	}
}

func TestGeminiWireFormat_VideoBytes(t *testing.T) {
	video, err := NewVideoFromBytes("video/mp4", []byte{0x00, 0x00, 0x00, 0x18})
	if err != nil {
		t.Fatal(err)
	}
	contents, _ := convertGeminiRequest(&Request{
		Messages: []Message{NewUserMessageWithBlocks(video)},
	})
	body, err := json.Marshal(contents[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if v := gjson.GetBytes(body, "parts.0.inlineData.mimeType").String(); v != "video/mp4" {
		t.Errorf("mimeType: got %q; body=%s", v, body)
	}
}

func TestGeminiWireFormat_PDFBytes(t *testing.T) {
	pdf, err := NewPDFFromBytes([]byte("%PDF-1.4"))
	if err != nil {
		t.Fatal(err)
	}
	contents, _ := convertGeminiRequest(&Request{
		Messages: []Message{NewUserMessageWithBlocks(pdf)},
	})
	body, _ := json.Marshal(contents[0])
	if v := gjson.GetBytes(body, "parts.0.inlineData.mimeType").String(); v != "application/pdf" {
		t.Errorf("mimeType: %q; body=%s", v, body)
	}
}

func TestGeminiWireFormat_AudioBytes(t *testing.T) {
	audio, err := NewAudioFromBytes("audio/mpeg", []byte{0xff, 0xfb})
	if err != nil {
		t.Fatal(err)
	}
	contents, _ := convertGeminiRequest(&Request{
		Messages: []Message{NewUserMessageWithBlocks(audio)},
	})
	body, _ := json.Marshal(contents[0])
	if v := gjson.GetBytes(body, "parts.0.inlineData.mimeType").String(); v != "audio/mpeg" {
		t.Errorf("mimeType: %q; body=%s", v, body)
	}
}
```

Add `"encoding/json"` and `"github.com/tidwall/gjson"` to the import block if not already present.

**Step 2: Run tests**

Run: `go test ./llm/... -run TestGeminiWireFormat -v` → 4/4 PASS.

**Step 3: Commit**

```bash
git add llm/gemini_test.go
git commit -m "test(llm): Gemini wire-format JSON snapshots for all four media kinds"
```

---

## Task 5: Ollama image-only — flip Capabilities + tests

**Files:**
- Modify: `llm/ollama.go`
- Modify: `llm/ollama_test.go`

**Step 1: Append failing tests**

Append to `llm/ollama_test.go`:

```go
func TestOllamaCapabilities(t *testing.T) {
	c := NewOllamaClient("", "")
	caps := c.Capabilities()
	if !caps.Image {
		t.Errorf("expected Image true, got %+v", caps)
	}
	if caps.PDF || caps.Audio || caps.Video {
		t.Errorf("expected PDF/Audio/Video false, got %+v", caps)
	}
}

func TestOllamaConvertRequest_ImageBytes(t *testing.T) {
	img, err := NewImageFromBytes("image/png", []byte{0x89, 'P', 'N', 'G'})
	if err != nil {
		t.Fatal(err)
	}
	params := convertOpenAIRequest(&Request{
		Messages: []Message{NewUserMessageWithBlocks(img)},
	})
	if len(params.Messages) != 1 || params.Messages[0].OfUser == nil {
		t.Fatalf("expected 1 user message, got %+v", params.Messages)
	}
	parts := params.Messages[0].OfUser.Content.OfArrayOfContentParts
	if len(parts) != 1 || parts[0].OfImageURL == nil {
		t.Fatalf("expected image_url part, got %+v", parts)
	}
	if !strings.HasPrefix(parts[0].OfImageURL.ImageURL.URL, "data:image/png;base64,") {
		t.Errorf("expected data URL prefix, got %q", parts[0].OfImageURL.ImageURL.URL)
	}
}

func TestOllamaCreateMessage_RejectsPDFMedia(t *testing.T) {
	c := NewOllamaClient("", "")
	pdf, err := NewPDFFromBytes([]byte("%PDF"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CreateMessage(context.Background(), &Request{
		Messages: []Message{NewUserMessageWithBlocks(pdf)},
	})
	var unsup *ErrUnsupportedMedia
	if !errors.As(err, &unsup) {
		t.Fatalf("expected *ErrUnsupportedMedia, got %T: %v", err, err)
	}
	if unsup.Provider != "ollama" || unsup.Media != "pdf" {
		t.Errorf("err fields: %+v", unsup)
	}
}
```

**Delete the now-stale test from PR #7**: remove `TestOllamaCreateMessage_RejectsImageMedia`. After flipping Capabilities, image is allowed.

**Step 2: Run — confirm fails**

Run: `go test ./llm/... -run TestOllama -v`
Expected: `TestOllamaCapabilities` fails (still returns empty Capabilities), other new tests pass or fail depending on whether the stale test is still present.

**Step 3: Flip Capabilities in `llm/ollama.go`**

Replace the existing `Capabilities()`:

```go
// Capabilities reports which media types Ollama supports as input.
// Ollama's OpenAI-compatible endpoint accepts image_url for vision-capable
// models (LLaVA, Llama 3.2 Vision, etc.). Documents, audio, and video are not
// supported on the chat endpoint regardless of model.
func (o *OllamaClient) Capabilities() Capabilities {
	return Capabilities{Image: true}
}
```

**Step 4: Run tests**

Run: `go test ./llm/... -run TestOllama -v` → all PASS.
Run: `go test ./...` → all packages pass.

**Step 5: Commit**

```bash
git add llm/ollama.go llm/ollama_test.go
git commit -m "feat(llm): Ollama image-only multimodal support"
```

---

## Task 6: OpenRouter image+PDF+audio — flip Capabilities + tests

**Files:**
- Modify: `llm/openrouter.go`
- Modify: `llm/openrouter_test.go`

**Step 1: Append failing tests**

Append to `llm/openrouter_test.go`:

```go
func TestOpenRouterCapabilities(t *testing.T) {
	c := NewOpenRouterClient("fake-key", "")
	caps := c.Capabilities()
	if !caps.Image || !caps.PDF || !caps.Audio {
		t.Errorf("expected Image+PDF+Audio true, got %+v", caps)
	}
	if caps.Video {
		t.Errorf("expected Video false, got %+v", caps)
	}
}

func TestOpenRouterConvertRequest_ImageBytes(t *testing.T) {
	img, err := NewImageFromBytes("image/png", []byte{0x89, 'P', 'N', 'G'})
	if err != nil {
		t.Fatal(err)
	}
	params := convertOpenAIRequest(&Request{
		Messages: []Message{NewUserMessageWithBlocks(img)},
	})
	parts := params.Messages[0].OfUser.Content.OfArrayOfContentParts
	if parts[0].OfImageURL == nil {
		t.Fatalf("expected image part, got %+v", parts[0])
	}
}

func TestOpenRouterCreateMessage_RejectsVideoMedia(t *testing.T) {
	c := NewOpenRouterClient("fake-key", "")
	video, err := NewVideoFromBytes("video/mp4", []byte{0x00, 0x00, 0x00, 0x18})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CreateMessage(context.Background(), &Request{
		Messages: []Message{NewUserMessageWithBlocks(video)},
	})
	var unsup *ErrUnsupportedMedia
	if !errors.As(err, &unsup) {
		t.Fatalf("expected *ErrUnsupportedMedia, got %T: %v", err, err)
	}
	if unsup.Provider != "openrouter" || unsup.Media != "video" {
		t.Errorf("err fields: %+v", unsup)
	}
}

func TestOpenRouterCreateMessage_PDFFromURL_ErrUnsupportedSource(t *testing.T) {
	c := NewOpenRouterClient("fake-key", "")
	pdf := NewPDFFromURL("https://example.com/x.pdf")
	_, err := c.CreateMessage(context.Background(), &Request{
		Messages: []Message{NewUserMessageWithBlocks(pdf)},
	})
	var ue *ErrUnsupportedSource
	if !errors.As(err, &ue) {
		t.Fatalf("expected *ErrUnsupportedSource, got %T: %v", err, err)
	}
	if ue.Media != "pdf" || ue.Kind != "url" {
		t.Errorf("err fields: %+v", ue)
	}
}
```

**Delete the now-stale test from PR #7**: remove `TestOpenRouterCreateMessage_RejectsImageMedia`.

**Step 2: Run — confirm fails**

Run: `go test ./llm/... -run TestOpenRouter -v`
Expected: capability + media-related tests fail until flip lands.

**Step 3: Flip Capabilities in `llm/openrouter.go`**

```go
// Capabilities reports which media types OpenRouter supports as input.
// OpenRouter routes to many upstream providers; we mirror OpenAI's matrix
// at the provider level (image + PDF + audio) and let upstream models 400
// on model-level mismatches per the design doc's "provider-level static +
// provider 400 fall-through" decision. Video is rejected because no major
// OpenRouter route currently accepts video on the chat endpoint.
func (o *OpenRouterClient) Capabilities() Capabilities {
	return Capabilities{Image: true, PDF: true, Audio: true}
}
```

**Step 4: Run tests**

Run: `go test ./llm/... -run TestOpenRouter -v` → all PASS.
Run: `go test ./...` → all packages pass.

**Step 5: Commit**

```bash
git add llm/openrouter.go llm/openrouter_test.go
git commit -m "feat(llm): OpenRouter multimodal support (image+PDF+audio)"
```

---

## Task 7: Full-suite verification

**Step 1: Vet**

Run: `go vet ./...`
Expected: no output.

**Step 2: Build**

Run: `go build ./...`
Expected: no output.

**Step 3: Full test run**

Run: `go test ./...`
Expected: all packages `ok`, no failures.

**Step 4: Race detector**

Run: `go test -race ./llm/...`
Expected: PASS.

**Step 5: Pre-commit hooks (verify they pass on all touched files)**

Run a no-op commit attempt to invoke the hooks — or just confirm by checking each Task's commit didn't fail. If anything fails, use superpowers:systematic-debugging.

If everything is green, the feature is ready for PR.

---

## Out of Scope (Phase 3 candidates)

- **URL auto-fetch on Gemini** — would let `NewVideoFromURL` work end-to-end
  via internal HTTP fetching. Rust port has it; SSRF tradeoffs deferred.
- **MCP tool-result image passthrough** — separate ergonomic improvement.
- **Per-model capability maps** — provider-level static + provider 400
  fall-through still wins.

See `docs/plans/2026-04-23-llm-multimodal-phase2-design.md` for full Phase 2
notes.
