# LLM Multimodal Input Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add image, PDF, and audio input support to `llm.Client` across the Anthropic and OpenAI providers, with a `Capabilities()` query and typed errors for unsupported media/source combinations.

**Architecture:** Extend the existing flat `ContentBlock` with a `MediaSource` pointer + `MediaType` string. Add factory constructors that read files eagerly and validate media types. Introduce `Capabilities()` on the `Client` interface so frontends can hide unsupported attachment UI. Pre-flight validation returns `ErrUnsupportedMedia` before any network call; provider translation emits `ErrUnsupportedSource` for source-form mismatches (OpenAI PDF/audio via URL).

**Tech Stack:** Go 1.23, `github.com/anthropics/anthropic-sdk-go` v1.19.0, `github.com/openai/openai-go` v1.12.0.

**Design doc:** `docs/plans/2026-04-21-llm-multimodal-input-design.md` — consult it if any decision below is unclear.

**Commands used throughout:**
- Build: `go build ./...`
- Full tests: `go test ./...`
- Package-focused: `go test ./llm/... -run <TestName> -v`
- Vet: `go vet ./...`

---

## Task 1: `MediaSource` type and `ContentBlock` extension

**Files:**
- Modify: `llm/types.go`
- Test: `llm/types_test.go` (new)

**Step 1: Write the failing test**

Create `llm/types_test.go`:

```go
// ABOUTME: Tests for ContentBlock media extensions, MediaSource variants,
// ABOUTME: and JSON round-trip behavior for multimodal content.
package llm

import (
	"encoding/json"
	"testing"
)

func TestContentBlock_MediaJSONRoundTrip_Bytes(t *testing.T) {
	original := ContentBlock{
		Type:      ContentTypeImage,
		MediaType: "image/png",
		Source: &MediaSource{
			Kind:  SourceKindBytes,
			Bytes: []byte{0x89, 0x50, 0x4E, 0x47},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored ContentBlock
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if restored.Type != ContentTypeImage {
		t.Errorf("Type: got %q want %q", restored.Type, ContentTypeImage)
	}
	if restored.MediaType != "image/png" {
		t.Errorf("MediaType: got %q want image/png", restored.MediaType)
	}
	if restored.Source == nil || restored.Source.Kind != SourceKindBytes {
		t.Fatalf("Source/Kind mismatch: %+v", restored.Source)
	}
	if len(restored.Source.Bytes) != 4 || restored.Source.Bytes[0] != 0x89 {
		t.Errorf("Bytes mismatch: %v", restored.Source.Bytes)
	}
}

func TestContentBlock_MediaJSONRoundTrip_URL(t *testing.T) {
	original := ContentBlock{
		Type:      ContentTypePDF,
		MediaType: "application/pdf",
		Source:    &MediaSource{Kind: SourceKindURL, URL: "https://example.com/file.pdf"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored ContentBlock
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if restored.Source == nil || restored.Source.URL != "https://example.com/file.pdf" {
		t.Errorf("URL mismatch: %+v", restored.Source)
	}
}

func TestContentBlock_TextHasNoSource(t *testing.T) {
	block := ContentBlock{Type: ContentTypeText, Text: "hi"}
	data, err := json.Marshal(block)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Source field should be absent (omitempty) for non-media blocks.
	var restored ContentBlock
	_ = json.Unmarshal(data, &restored)
	if restored.Source != nil {
		t.Errorf("Source should be nil for text block, got %+v", restored.Source)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./llm/... -run TestContentBlock_Media -v`
Expected: compilation error — `ContentTypeImage`, `ContentTypePDF`, `MediaSource`, `SourceKindBytes`, `SourceKindURL` undefined.

**Step 3: Add types to `llm/types.go`**

Add the three new `ContentType` constants to the existing block (keep `ContentTypeText`, `ContentTypeToolUse`, `ContentTypeToolResult` as-is):

```go
const (
	ContentTypeText       ContentType = "text"
	ContentTypeToolUse    ContentType = "tool_use"
	ContentTypeToolResult ContentType = "tool_result"
	ContentTypeImage      ContentType = "image"
	ContentTypePDF        ContentType = "pdf"
	ContentTypeAudio      ContentType = "audio"
)
```

Add `SourceKind` and `MediaSource` after the existing type declarations but before `Message`:

```go
// SourceKind identifies how media bytes are supplied.
type SourceKind string

const (
	SourceKindURL   SourceKind = "url"
	SourceKindBytes SourceKind = "bytes"
	SourceKindFile  SourceKind = "file"
)

// MediaSource describes where media bytes come from.
// For SourceKindFile, Bytes is populated eagerly at construction time;
// Path remains set for informational use (filenames on provider APIs, UI display).
type MediaSource struct {
	Kind  SourceKind `json:"kind"`
	URL   string     `json:"url,omitempty"`
	Bytes []byte     `json:"bytes,omitempty"`
	Path  string     `json:"path,omitempty"`
}
```

Extend `ContentBlock` with the two new fields (add after `IsError`):

```go
// For media content (image, pdf, audio)
Source    *MediaSource `json:"source,omitempty"`
MediaType string       `json:"media_type,omitempty"`
```

**Step 4: Run test to verify it passes**

Run: `go test ./llm/... -run TestContentBlock -v`
Expected: PASS for all three tests.

**Step 5: Commit**

```bash
git add llm/types.go llm/types_test.go
git commit -m "feat(llm): add MediaSource type and ContentBlock media fields"
```

---

## Task 2: Typed errors

**Files:**
- Modify: `llm/types.go`
- Test: `llm/types_test.go`

**Step 1: Append failing tests**

Add to `llm/types_test.go`:

```go
import "errors"

func TestErrUnsupportedMedia_Error(t *testing.T) {
	err := &ErrUnsupportedMedia{Provider: "anthropic", Media: "audio"}
	if err.Error() != "anthropic does not support media type \"audio\"" {
		t.Errorf("message: %q", err.Error())
	}
}

func TestErrUnsupportedMedia_ErrorsAs(t *testing.T) {
	err := error(&ErrUnsupportedMedia{Provider: "anthropic", Media: "audio"})
	var target *ErrUnsupportedMedia
	if !errors.As(err, &target) {
		t.Fatal("errors.As should match")
	}
	if target.Provider != "anthropic" || target.Media != "audio" {
		t.Errorf("target fields wrong: %+v", target)
	}
}

func TestErrUnsupportedSource_Error(t *testing.T) {
	err := &ErrUnsupportedSource{Provider: "openai", Media: "audio", Kind: "url"}
	want := "openai does not support source kind \"url\" for media type \"audio\""
	if err.Error() != want {
		t.Errorf("message: got %q want %q", err.Error(), want)
	}
}

func TestErrUnsupportedSource_ErrorsAs(t *testing.T) {
	err := error(&ErrUnsupportedSource{Provider: "openai", Media: "pdf", Kind: "url"})
	var target *ErrUnsupportedSource
	if !errors.As(err, &target) {
		t.Fatal("errors.As should match")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./llm/... -run TestErrUnsupported -v`
Expected: compilation error — types undefined.

**Step 3: Add error types to `llm/types.go`**

```go
import "fmt"  // add to existing imports

// ErrUnsupportedMedia indicates a provider cannot handle the requested media type at all.
type ErrUnsupportedMedia struct {
	Provider string
	Media    string
}

func (e *ErrUnsupportedMedia) Error() string {
	return fmt.Sprintf("%s does not support media type %q", e.Provider, e.Media)
}

// ErrUnsupportedSource indicates a provider supports the media type but not this source form.
type ErrUnsupportedSource struct {
	Provider string
	Media    string
	Kind     string
}

func (e *ErrUnsupportedSource) Error() string {
	return fmt.Sprintf("%s does not support source kind %q for media type %q", e.Provider, e.Kind, e.Media)
}
```

**Step 4: Run tests**

Run: `go test ./llm/... -run TestErrUnsupported -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add llm/types.go llm/types_test.go
git commit -m "feat(llm): add typed errors for unsupported media and source forms"
```

---

## Task 3: `Capabilities` struct and helper

**Files:**
- Modify: `llm/types.go`
- Test: `llm/types_test.go`

**Step 1: Append failing test**

```go
func TestFullCapabilities(t *testing.T) {
	caps := FullCapabilities()
	if !caps.Image || !caps.PDF || !caps.Audio || !caps.Video {
		t.Errorf("FullCapabilities should enable everything: %+v", caps)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./llm/... -run TestFullCapabilities -v`
Expected: compilation error — `FullCapabilities` undefined.

**Step 3: Add to `llm/types.go`**

```go
// Capabilities describes which media types a provider supports as input.
type Capabilities struct {
	Image bool `json:"image"`
	PDF   bool `json:"pdf"`
	Audio bool `json:"audio"`
	Video bool `json:"video"`
}

// FullCapabilities returns a Capabilities with every media type enabled.
// Useful for test mocks that don't care about capability-gated behavior.
func FullCapabilities() Capabilities {
	return Capabilities{Image: true, PDF: true, Audio: true, Video: true}
}
```

**Step 4: Run test**

Run: `go test ./llm/... -run TestFullCapabilities -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add llm/types.go llm/types_test.go
git commit -m "feat(llm): add Capabilities struct and FullCapabilities helper"
```

---

## Task 4: Extend `Client` interface with `Capabilities()` and update all implementors

This is the breaking interface change. After this task, `go build ./...` must still pass.

**Files:**
- Modify: `llm/client.go`
- Modify: `llm/anthropic.go`
- Modify: `llm/openai.go`
- Modify: `llm/llm_test.go` (mock)
- Modify: `orchestrator/orchestrator_test.go` (three mocks)
- Modify: `agent/agent_test.go` (mock)
- Modify: `integration_test.go` (mock)
- Modify: `examples/simple/main.go` (DemoClient)

**Step 1: Add method to interface**

In `llm/client.go`:

```go
type Client interface {
	CreateMessage(ctx context.Context, req *Request) (*Response, error)
	CreateMessageStream(ctx context.Context, req *Request) (<-chan StreamEvent, error)
	Capabilities() Capabilities
}
```

**Step 2: Run build — expect failures**

Run: `go build ./...`
Expected: fails in `llm/anthropic.go`, `llm/openai.go`, plus every mock, with "does not implement Client (missing method Capabilities)".

**Step 3: Implement on real clients**

At the bottom of `llm/anthropic.go` (just before or after the `var _ Client = ...` assertion):

```go
// Capabilities reports which media types Anthropic supports in our Messages API.
func (a *AnthropicClient) Capabilities() Capabilities {
	return Capabilities{Image: true, PDF: true, Audio: false, Video: false}
}
```

At the bottom of `llm/openai.go`:

```go
// Capabilities reports which media types OpenAI's Chat Completions supports.
// Audio is provider-level enabled; specific models (gpt-4o-audio-preview class)
// are required at send time — the API rejects on mismatch.
func (o *OpenAIClient) Capabilities() Capabilities {
	return Capabilities{Image: true, PDF: true, Audio: true, Video: false}
}
```

**Step 4: Update every in-repo mock**

For each mock, add the method returning `llm.FullCapabilities()`. Specific locations:

- `llm/llm_test.go`: mock at line 12. Add `func (m *mockClient) Capabilities() Capabilities { return FullCapabilities() }` (inside the `llm` package — no `llm.` prefix).

- `orchestrator/orchestrator_test.go`: three mocks — `mockLLMClient` (line 162), `mockLLMClientWithError` (line 793), `mockLoopingLLMClient` (line 804). Add `func (m *<name>) Capabilities() llm.Capabilities { return llm.FullCapabilities() }` to each.

- `agent/agent_test.go`: `mockClient` at line 21. Add `func (m *mockClient) Capabilities() llm.Capabilities { return llm.FullCapabilities() }`.

- `integration_test.go`: `mockLLM` at line 49. Add `func (m *mockLLM) Capabilities() llm.Capabilities { return llm.FullCapabilities() }`.

- `examples/simple/main.go`: `DemoClient` at line 29. Add `func (c *DemoClient) Capabilities() llm.Capabilities { return llm.FullCapabilities() }`.

**Step 5: Build and test**

Run: `go build ./...`
Expected: success.

Run: `go test ./...`
Expected: all existing tests still pass.

**Step 6: Add capability tests for real clients**

Add to `llm/anthropic_test.go`:

```go
func TestAnthropicClient_Capabilities(t *testing.T) {
	c := NewAnthropicClient("key", "")
	caps := c.Capabilities()
	if !caps.Image || !caps.PDF {
		t.Errorf("expected Image+PDF true, got %+v", caps)
	}
	if caps.Audio || caps.Video {
		t.Errorf("expected Audio+Video false, got %+v", caps)
	}
}
```

Add to `llm/openai_test.go`:

```go
func TestOpenAIClient_Capabilities(t *testing.T) {
	c := NewOpenAIClient("key", "")
	caps := c.Capabilities()
	if !caps.Image || !caps.PDF || !caps.Audio {
		t.Errorf("expected Image+PDF+Audio true, got %+v", caps)
	}
	if caps.Video {
		t.Errorf("expected Video false, got %+v", caps)
	}
}
```

Run: `go test ./llm/... -run Capabilities -v`
Expected: PASS.

**Step 7: Commit**

```bash
git add llm/ orchestrator/ agent/ integration_test.go examples/simple/main.go
git commit -m "feat(llm): add Capabilities() to Client interface"
```

---

## Task 5: Image constructors

**Files:**
- Modify: `llm/types.go`
- Test: `llm/types_test.go`
- Create: `llm/testdata/tiny.png`

**Step 1: Create test fixture**

Create `llm/testdata/tiny.png` — a 67-byte 1×1 transparent PNG. Use this shell:

```bash
mkdir -p llm/testdata
printf '\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\xcf\xc0\x00\x00\x00\x03\x00\x01[\xb7\xbf\xc4\x00\x00\x00\x00IEND\xaeB`\x82' > llm/testdata/tiny.png
```

Verify: `file llm/testdata/tiny.png` should report `PNG image data, 1 x 1`.

**Step 2: Write failing tests**

Append to `llm/types_test.go`:

```go
import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

func TestNewImageFromURL(t *testing.T) {
	b := NewImageFromURL("https://example.com/cat.png")
	if b.Type != ContentTypeImage {
		t.Errorf("Type: %q", b.Type)
	}
	if b.Source == nil || b.Source.Kind != SourceKindURL || b.Source.URL != "https://example.com/cat.png" {
		t.Errorf("source: %+v", b.Source)
	}
	if b.MediaType != "" {
		t.Errorf("URL form should not set MediaType, got %q", b.MediaType)
	}
}

func TestNewImageFromBytes_OK(t *testing.T) {
	b, err := NewImageFromBytes("image/png", []byte{0x89, 0x50, 0x4E, 0x47})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.Type != ContentTypeImage || b.MediaType != "image/png" {
		t.Errorf("block: %+v", b)
	}
	if b.Source.Kind != SourceKindBytes || len(b.Source.Bytes) != 4 {
		t.Errorf("source: %+v", b.Source)
	}
}

func TestNewImageFromBytes_RejectsNonImage(t *testing.T) {
	_, err := NewImageFromBytes("audio/mpeg", []byte{0x01, 0x02})
	if err == nil {
		t.Fatal("expected error for non-image media type")
	}
	if !strings.Contains(err.Error(), "image/") {
		t.Errorf("error message should mention image/, got %q", err.Error())
	}
}

func TestNewImageFromBytes_RejectsEmpty(t *testing.T) {
	_, err := NewImageFromBytes("image/png", nil)
	if err == nil {
		t.Fatal("expected error for empty bytes")
	}
}

func TestNewImageFromFile_OK(t *testing.T) {
	b, err := NewImageFromFile(filepath.Join("testdata", "tiny.png"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.Type != ContentTypeImage {
		t.Errorf("Type: %q", b.Type)
	}
	if b.MediaType != "image/png" {
		t.Errorf("MediaType: %q want image/png", b.MediaType)
	}
	if b.Source.Kind != SourceKindFile {
		t.Errorf("Kind: %q", b.Source.Kind)
	}
	if !bytes.HasPrefix(b.Source.Bytes, []byte{0x89, 'P', 'N', 'G'}) {
		t.Errorf("bytes should have PNG magic, got %v", b.Source.Bytes[:8])
	}
	if !strings.HasSuffix(b.Source.Path, "tiny.png") {
		t.Errorf("Path retained: %q", b.Source.Path)
	}
}

func TestNewImageFromFile_MissingFile(t *testing.T) {
	_, err := NewImageFromFile("testdata/does-not-exist.png")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !os.IsNotExist(err) && !strings.Contains(err.Error(), "no such file") {
		t.Errorf("expected not-exist error, got %v", err)
	}
}

func TestNewImageFromFile_UnknownExtension(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "weird.xyz")
	if err := os.WriteFile(p, []byte("blob"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := NewImageFromFile(p)
	if err == nil {
		t.Fatal("expected error for unknown extension")
	}
}
```

**Step 3: Run tests to verify they fail**

Run: `go test ./llm/... -run TestNewImage -v`
Expected: compilation errors for undefined constructors.

**Step 4: Implement in `llm/types.go`**

Add imports: `"fmt"` (already added), `"mime"`, `"os"`, `"path/filepath"`, `"strings"`.

```go
// NewImageFromURL constructs an image content block backed by a remote URL.
func NewImageFromURL(url string) ContentBlock {
	return ContentBlock{
		Type:   ContentTypeImage,
		Source: &MediaSource{Kind: SourceKindURL, URL: url},
	}
}

// NewImageFromBytes constructs an image content block from inline bytes.
// mediaType must be an image/* MIME type; data must be non-empty.
func NewImageFromBytes(mediaType string, data []byte) (ContentBlock, error) {
	if err := validateMediaFamily("image", mediaType); err != nil {
		return ContentBlock{}, err
	}
	if len(data) == 0 {
		return ContentBlock{}, fmt.Errorf("NewImageFromBytes: data is empty")
	}
	return ContentBlock{
		Type:      ContentTypeImage,
		MediaType: mediaType,
		Source:    &MediaSource{Kind: SourceKindBytes, Bytes: data},
	}, nil
}

// NewImageFromFile reads an image file, infers its media type from the
// extension, and returns a ready-to-send content block.
func NewImageFromFile(path string) (ContentBlock, error) {
	data, mediaType, err := readMediaFile(path, "image")
	if err != nil {
		return ContentBlock{}, err
	}
	return ContentBlock{
		Type:      ContentTypeImage,
		MediaType: mediaType,
		Source:    &MediaSource{Kind: SourceKindFile, Bytes: data, Path: path},
	}, nil
}

// validateMediaFamily confirms mediaType starts with the expected family prefix
// (e.g. "image/", "audio/") or matches exactly for fixed types.
func validateMediaFamily(family, mediaType string) error {
	if family == "pdf" {
		if mediaType != "application/pdf" {
			return fmt.Errorf("media type %q is not application/pdf", mediaType)
		}
		return nil
	}
	prefix := family + "/"
	if !strings.HasPrefix(mediaType, prefix) {
		return fmt.Errorf("media type %q is not in family %s*", mediaType, prefix)
	}
	return nil
}

// readMediaFile reads a file, infers its media type from the extension, and
// validates the media type against the expected family.
func readMediaFile(path, family string) (data []byte, mediaType string, err error) {
	data, err = os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	ext := filepath.Ext(path)
	mediaType = mime.TypeByExtension(ext)
	if i := strings.Index(mediaType, ";"); i != -1 {
		mediaType = mediaType[:i]
	}
	if mediaType == "" {
		return nil, "", fmt.Errorf("could not infer media type from extension %q", ext)
	}
	if err := validateMediaFamily(family, mediaType); err != nil {
		return nil, "", err
	}
	return data, mediaType, nil
}
```

**Step 5: Run tests**

Run: `go test ./llm/... -run TestNewImage -v`
Expected: PASS all six.

**Step 6: Commit**

```bash
git add llm/types.go llm/types_test.go llm/testdata/tiny.png
git commit -m "feat(llm): add image content-block constructors"
```

---

## Task 6: PDF constructors

**Files:**
- Modify: `llm/types.go`
- Test: `llm/types_test.go`
- Create: `llm/testdata/tiny.pdf`

**Step 1: Create fixture**

```bash
printf '%%PDF-1.4\n1 0 obj\n<<>>\nendobj\nxref\n0 1\n0000000000 65535 f \ntrailer\n<< /Size 1 /Root 1 0 R >>\nstartxref\n0\n%%%%EOF\n' > llm/testdata/tiny.pdf
```

Verify: `file llm/testdata/tiny.pdf` reports a PDF.

**Step 2: Append failing tests**

```go
func TestNewPDFFromURL(t *testing.T) {
	b := NewPDFFromURL("https://example.com/x.pdf")
	if b.Type != ContentTypePDF || b.Source.Kind != SourceKindURL {
		t.Errorf("block: %+v", b)
	}
}

func TestNewPDFFromBytes_OK(t *testing.T) {
	b, err := NewPDFFromBytes([]byte("%PDF-1.4\n"))
	if err != nil {
		t.Fatal(err)
	}
	if b.MediaType != "application/pdf" {
		t.Errorf("MediaType: %q", b.MediaType)
	}
}

func TestNewPDFFromBytes_RejectsEmpty(t *testing.T) {
	if _, err := NewPDFFromBytes(nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewPDFFromFile_OK(t *testing.T) {
	b, err := NewPDFFromFile(filepath.Join("testdata", "tiny.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if b.MediaType != "application/pdf" {
		t.Errorf("MediaType: %q", b.MediaType)
	}
	if !bytes.HasPrefix(b.Source.Bytes, []byte("%PDF")) {
		t.Errorf("bytes should start with %%PDF")
	}
}

func TestNewPDFFromFile_WrongExtension(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "foo.png")
	_ = os.WriteFile(p, []byte("x"), 0o644)
	if _, err := NewPDFFromFile(p); err == nil {
		t.Fatal("expected error for non-pdf extension")
	}
}
```

**Step 3: Run tests (fails: undefined)**

Run: `go test ./llm/... -run TestNewPDF -v`
Expected: compilation error.

**Step 4: Implement**

Add to `llm/types.go`:

```go
// NewPDFFromURL constructs a PDF content block backed by a remote URL.
func NewPDFFromURL(url string) ContentBlock {
	return ContentBlock{
		Type:      ContentTypePDF,
		MediaType: "application/pdf",
		Source:    &MediaSource{Kind: SourceKindURL, URL: url},
	}
}

// NewPDFFromBytes constructs a PDF content block from inline bytes.
func NewPDFFromBytes(data []byte) (ContentBlock, error) {
	if len(data) == 0 {
		return ContentBlock{}, fmt.Errorf("NewPDFFromBytes: data is empty")
	}
	return ContentBlock{
		Type:      ContentTypePDF,
		MediaType: "application/pdf",
		Source:    &MediaSource{Kind: SourceKindBytes, Bytes: data},
	}, nil
}

// NewPDFFromFile reads a PDF file and returns a ready-to-send content block.
func NewPDFFromFile(path string) (ContentBlock, error) {
	data, mediaType, err := readMediaFile(path, "pdf")
	if err != nil {
		return ContentBlock{}, err
	}
	return ContentBlock{
		Type:      ContentTypePDF,
		MediaType: mediaType,
		Source:    &MediaSource{Kind: SourceKindFile, Bytes: data, Path: path},
	}, nil
}
```

**Step 5: Run tests**

Run: `go test ./llm/... -run TestNewPDF -v`
Expected: PASS.

**Step 6: Commit**

```bash
git add llm/types.go llm/types_test.go llm/testdata/tiny.pdf
git commit -m "feat(llm): add PDF content-block constructors"
```

---

## Task 7: Audio constructors

**Files:**
- Modify: `llm/types.go`
- Test: `llm/types_test.go`
- Create: `llm/testdata/tiny.mp3`

**Step 1: Create fixture**

```bash
# 4-byte synthetic MP3 frame header (enough for our translation tests; providers
# will reject it as playable audio, but we never send it through).
printf '\xff\xfb\x90\x00' > llm/testdata/tiny.mp3
```

**Step 2: Append failing tests**

```go
func TestNewAudioFromURL(t *testing.T) {
	b := NewAudioFromURL("https://example.com/a.mp3")
	if b.Type != ContentTypeAudio || b.Source.Kind != SourceKindURL {
		t.Errorf("block: %+v", b)
	}
}

func TestNewAudioFromBytes_OK(t *testing.T) {
	b, err := NewAudioFromBytes("audio/mpeg", []byte{0xff, 0xfb})
	if err != nil {
		t.Fatal(err)
	}
	if b.MediaType != "audio/mpeg" {
		t.Errorf("MediaType: %q", b.MediaType)
	}
}

func TestNewAudioFromBytes_RejectsNonAudio(t *testing.T) {
	if _, err := NewAudioFromBytes("image/png", []byte{0x01}); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewAudioFromFile_OK(t *testing.T) {
	b, err := NewAudioFromFile(filepath.Join("testdata", "tiny.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(b.MediaType, "audio/") {
		t.Errorf("MediaType: %q", b.MediaType)
	}
}
```

**Step 3: Run tests (fails)**

Run: `go test ./llm/... -run TestNewAudio -v`
Expected: compile error.

**Step 4: Implement**

```go
// NewAudioFromURL constructs an audio content block backed by a remote URL.
func NewAudioFromURL(url string) ContentBlock {
	return ContentBlock{
		Type:   ContentTypeAudio,
		Source: &MediaSource{Kind: SourceKindURL, URL: url},
	}
}

// NewAudioFromBytes constructs an audio content block from inline bytes.
// mediaType must be an audio/* MIME type.
func NewAudioFromBytes(mediaType string, data []byte) (ContentBlock, error) {
	if err := validateMediaFamily("audio", mediaType); err != nil {
		return ContentBlock{}, err
	}
	if len(data) == 0 {
		return ContentBlock{}, fmt.Errorf("NewAudioFromBytes: data is empty")
	}
	return ContentBlock{
		Type:      ContentTypeAudio,
		MediaType: mediaType,
		Source:    &MediaSource{Kind: SourceKindBytes, Bytes: data},
	}, nil
}

// NewAudioFromFile reads an audio file and returns a ready-to-send content block.
func NewAudioFromFile(path string) (ContentBlock, error) {
	data, mediaType, err := readMediaFile(path, "audio")
	if err != nil {
		return ContentBlock{}, err
	}
	return ContentBlock{
		Type:      ContentTypeAudio,
		MediaType: mediaType,
		Source:    &MediaSource{Kind: SourceKindFile, Bytes: data, Path: path},
	}, nil
}
```

**Step 5: Run tests**

Run: `go test ./llm/... -run TestNewAudio -v`
Expected: PASS.

**Step 6: Commit**

```bash
git add llm/types.go llm/types_test.go llm/testdata/tiny.mp3
git commit -m "feat(llm): add audio content-block constructors"
```

---

## Task 8: `NewUserMessageWithBlocks` convenience + `validateRequest`

**Files:**
- Modify: `llm/types.go`
- Create: `llm/validate.go`
- Modify: `llm/types_test.go`

**Step 1: Write failing tests**

Add to `llm/types_test.go`:

```go
func TestNewUserMessageWithBlocks(t *testing.T) {
	img := NewImageFromURL("https://example.com/x.png")
	text := ContentBlock{Type: ContentTypeText, Text: "describe this"}
	msg := NewUserMessageWithBlocks(text, img)
	if msg.Role != RoleUser {
		t.Errorf("Role: %q", msg.Role)
	}
	if len(msg.Blocks) != 2 || msg.Blocks[0].Type != ContentTypeText || msg.Blocks[1].Type != ContentTypeImage {
		t.Errorf("Blocks: %+v", msg.Blocks)
	}
}

func TestValidateRequest_AllowsSupportedMedia(t *testing.T) {
	caps := Capabilities{Image: true, PDF: true, Audio: true}
	req := &Request{
		Messages: []Message{
			NewUserMessageWithBlocks(NewImageFromURL("https://x"), mustPDFBytes(t)),
		},
	}
	if err := validateRequest("test", caps, req); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateRequest_RejectsUnsupportedMedia(t *testing.T) {
	caps := Capabilities{Image: true, PDF: true, Audio: false}
	req := &Request{
		Messages: []Message{
			{Role: RoleUser, Blocks: []ContentBlock{NewAudioFromURL("https://x.mp3")}},
		},
	}
	err := validateRequest("anthropic", caps, req)
	var unsup *ErrUnsupportedMedia
	if !errors.As(err, &unsup) {
		t.Fatalf("expected *ErrUnsupportedMedia, got %T: %v", err, err)
	}
	if unsup.Provider != "anthropic" || unsup.Media != "audio" {
		t.Errorf("err fields: %+v", unsup)
	}
}

func TestValidateRequest_IgnoresNonMediaBlocks(t *testing.T) {
	caps := Capabilities{} // no media support at all
	req := &Request{
		Messages: []Message{
			NewUserMessage("hi"),
			{Role: RoleAssistant, Blocks: []ContentBlock{{Type: ContentTypeToolUse, ID: "1", Name: "t"}}},
		},
	}
	if err := validateRequest("test", caps, req); err != nil {
		t.Errorf("text/tool blocks shouldn't trigger media validation: %v", err)
	}
}

func mustPDFBytes(t *testing.T) ContentBlock {
	t.Helper()
	b, err := NewPDFFromBytes([]byte("%PDF"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
```

**Step 2: Run (fails)**

Run: `go test ./llm/... -run "TestNewUserMessageWithBlocks|TestValidateRequest" -v`
Expected: compile error.

**Step 3: Implement helper in `llm/types.go`**

```go
// NewUserMessageWithBlocks constructs a user message from one or more content blocks.
// Use this when assembling multimodal messages (text + image, PDF, etc.).
func NewUserMessageWithBlocks(blocks ...ContentBlock) Message {
	return Message{Role: RoleUser, Blocks: blocks}
}
```

**Step 4: Create `llm/validate.go`**

```go
// ABOUTME: Pre-flight validation that checks a Request's content blocks
// ABOUTME: against a provider's Capabilities before any network call.
package llm

// validateRequest returns ErrUnsupportedMedia if any message block uses a
// media type the provider can't handle. Non-media blocks (text, tool_use,
// tool_result) are ignored.
func validateRequest(provider string, caps Capabilities, req *Request) error {
	for _, msg := range req.Messages {
		for _, block := range msg.Blocks {
			if err := checkBlock(provider, caps, block); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkBlock(provider string, caps Capabilities, block ContentBlock) error {
	switch block.Type {
	case ContentTypeImage:
		if !caps.Image {
			return &ErrUnsupportedMedia{Provider: provider, Media: "image"}
		}
	case ContentTypePDF:
		if !caps.PDF {
			return &ErrUnsupportedMedia{Provider: provider, Media: "pdf"}
		}
	case ContentTypeAudio:
		if !caps.Audio {
			return &ErrUnsupportedMedia{Provider: provider, Media: "audio"}
		}
	}
	return nil
}
```

**Step 5: Run tests**

Run: `go test ./llm/... -run "TestNewUserMessageWithBlocks|TestValidateRequest" -v`
Expected: PASS all four.

**Step 6: Commit**

```bash
git add llm/types.go llm/types_test.go llm/validate.go
git commit -m "feat(llm): add validateRequest pre-flight capability check"
```

---

## Task 9: Anthropic image + PDF translation + validation wiring

**Files:**
- Modify: `llm/anthropic.go`
- Modify: `llm/anthropic_test.go`

**Step 1: Write failing tests**

Append to `llm/anthropic_test.go`:

```go
func TestAnthropicConvertRequest_ImageBytes(t *testing.T) {
	img, err := NewImageFromBytes("image/png", []byte{0x89, 'P', 'N', 'G'})
	if err != nil {
		t.Fatal(err)
	}
	req := &Request{
		Model:    "claude-sonnet-4-20250514",
		Messages: []Message{NewUserMessageWithBlocks(img)},
	}
	params := convertRequest(req)
	if len(params.Messages) != 1 || len(params.Messages[0].Content) != 1 {
		t.Fatalf("expected 1 content block, got %+v", params.Messages)
	}
	block := params.Messages[0].Content[0]
	if block.OfImage == nil {
		t.Fatalf("expected OfImage set, got %+v", block)
	}
	if block.OfImage.Source.OfBase64 == nil {
		t.Fatalf("expected base64 source")
	}
	if string(block.OfImage.Source.OfBase64.MediaType) != "image/png" {
		t.Errorf("media type: %q", block.OfImage.Source.OfBase64.MediaType)
	}
	// "iVBORw==" is base64("\x89PNG"); just assert non-empty.
	if block.OfImage.Source.OfBase64.Data == "" {
		t.Error("expected non-empty base64 data")
	}
}

func TestAnthropicConvertRequest_ImageURL(t *testing.T) {
	img := NewImageFromURL("https://example.com/cat.png")
	req := &Request{Messages: []Message{NewUserMessageWithBlocks(img)}}
	params := convertRequest(req)
	block := params.Messages[0].Content[0]
	if block.OfImage == nil || block.OfImage.Source.OfURL == nil {
		t.Fatalf("expected URL image source, got %+v", block.OfImage)
	}
	if block.OfImage.Source.OfURL.URL != "https://example.com/cat.png" {
		t.Errorf("URL: %q", block.OfImage.Source.OfURL.URL)
	}
}

func TestAnthropicConvertRequest_PDFBytes(t *testing.T) {
	pdf, err := NewPDFFromBytes([]byte("%PDF-1.4"))
	if err != nil {
		t.Fatal(err)
	}
	req := &Request{Messages: []Message{NewUserMessageWithBlocks(pdf)}}
	params := convertRequest(req)
	block := params.Messages[0].Content[0]
	if block.OfDocument == nil || block.OfDocument.Source.OfBase64 == nil {
		t.Fatalf("expected base64 PDF source, got %+v", block.OfDocument)
	}
	if block.OfDocument.Source.OfBase64.Data == "" {
		t.Error("expected non-empty base64 data")
	}
}

func TestAnthropicConvertRequest_PDFURL(t *testing.T) {
	pdf := NewPDFFromURL("https://example.com/x.pdf")
	req := &Request{Messages: []Message{NewUserMessageWithBlocks(pdf)}}
	params := convertRequest(req)
	block := params.Messages[0].Content[0]
	if block.OfDocument == nil || block.OfDocument.Source.OfURL == nil {
		t.Fatalf("expected URL PDF source")
	}
	if block.OfDocument.Source.OfURL.URL != "https://example.com/x.pdf" {
		t.Errorf("URL: %q", block.OfDocument.Source.OfURL.URL)
	}
}

func TestAnthropicCreateMessage_RejectsAudio(t *testing.T) {
	audio := NewAudioFromURL("https://example.com/a.mp3")
	c := NewAnthropicClient("fake-key", "")
	req := &Request{Messages: []Message{NewUserMessageWithBlocks(audio)}}
	_, err := c.CreateMessage(context.Background(), req)
	var unsup *ErrUnsupportedMedia
	if !errors.As(err, &unsup) {
		t.Fatalf("expected *ErrUnsupportedMedia, got %T: %v", err, err)
	}
	if unsup.Provider != "anthropic" || unsup.Media != "audio" {
		t.Errorf("err fields: %+v", unsup)
	}
}

func TestAnthropicCreateMessageStream_RejectsAudio(t *testing.T) {
	audio := NewAudioFromURL("https://example.com/a.mp3")
	c := NewAnthropicClient("fake-key", "")
	req := &Request{Messages: []Message{NewUserMessageWithBlocks(audio)}}
	_, err := c.CreateMessageStream(context.Background(), req)
	var unsup *ErrUnsupportedMedia
	if !errors.As(err, &unsup) {
		t.Fatalf("expected *ErrUnsupportedMedia, got %T: %v", err, err)
	}
}
```

Ensure `context` and `errors` are imported in the test file.

**Step 2: Run (fails)**

Run: `go test ./llm/... -run TestAnthropic -v`
Expected: the four `ConvertRequest_*` tests fail (translation doesn't handle media yet); the two `RejectsAudio` tests fail (no validation wired).

**Step 3: Wire validation in `llm/anthropic.go`**

In `CreateMessage`, add before `convertRequest`:

```go
if err := validateRequest("anthropic", a.Capabilities(), req); err != nil {
	return nil, err
}
```

Same in `CreateMessageStream`:

```go
if err := validateRequest("anthropic", a.Capabilities(), req); err != nil {
	return nil, err
}
```

**Step 4: Extend `convertRequest` media cases**

Add `"encoding/base64"` to imports. Inside the block-switch loop in `convertRequest` (currently: `text`, `tool_use`, `tool_result`), add cases:

```go
case ContentTypeImage:
	content = append(content, convertAnthropicImage(block))
case ContentTypePDF:
	content = append(content, convertAnthropicPDF(block))
```

Add helpers at the bottom of the file:

```go
func convertAnthropicImage(block ContentBlock) anthropic.ContentBlockParamUnion {
	if block.Source == nil {
		return anthropic.NewTextBlock("") // unreachable: validated upstream
	}
	switch block.Source.Kind {
	case SourceKindURL:
		return anthropic.NewImageBlock(anthropic.URLImageSourceParam{URL: block.Source.URL})
	default: // Bytes or File (File's Bytes populated eagerly)
		encoded := base64.StdEncoding.EncodeToString(block.Source.Bytes)
		return anthropic.NewImageBlockBase64(block.MediaType, encoded)
	}
}

func convertAnthropicPDF(block ContentBlock) anthropic.ContentBlockParamUnion {
	if block.Source == nil {
		return anthropic.NewTextBlock("")
	}
	switch block.Source.Kind {
	case SourceKindURL:
		return anthropic.NewDocumentBlock(anthropic.URLPDFSourceParam{URL: block.Source.URL})
	default:
		encoded := base64.StdEncoding.EncodeToString(block.Source.Bytes)
		return anthropic.NewDocumentBlock(anthropic.Base64PDFSourceParam{Data: encoded})
	}
}
```

**Step 5: Run tests**

Run: `go test ./llm/... -run TestAnthropic -v`
Expected: PASS including all existing Anthropic tests (no regression).

Run: `go test ./...`
Expected: PASS.

**Step 6: Commit**

```bash
git add llm/anthropic.go llm/anthropic_test.go
git commit -m "feat(llm): Anthropic image/PDF translation + pre-flight validation"
```

---

## Task 10: OpenAI multipart user message refactor + image translation

This task refactors `convertUserMessage` to assemble a `[]ChatCompletionContentPartUnionParam` instead of returning a single text/tool message. Existing text-only paths must continue to work.

**Files:**
- Modify: `llm/openai.go`
- Modify: `llm/openai_test.go`

**Step 1: Write failing tests**

Append to `llm/openai_test.go`:

```go
func TestOpenAIConvertUserMessage_ImageBytes(t *testing.T) {
	img, err := NewImageFromBytes("image/png", []byte{0x89, 'P', 'N', 'G'})
	if err != nil {
		t.Fatal(err)
	}
	msg := convertUserMessage(NewUserMessageWithBlocks(img))
	if msg.OfUser == nil {
		t.Fatalf("expected user message, got %+v", msg)
	}
	parts := msg.OfUser.Content.OfArrayOfContentParts
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	if parts[0].OfImageURL == nil {
		t.Fatalf("expected image part, got %+v", parts[0])
	}
	if !strings.HasPrefix(parts[0].OfImageURL.ImageURL.URL, "data:image/png;base64,") {
		t.Errorf("expected data URL, got %q", parts[0].OfImageURL.ImageURL.URL)
	}
}

func TestOpenAIConvertUserMessage_ImageURL(t *testing.T) {
	img := NewImageFromURL("https://example.com/x.png")
	msg := convertUserMessage(NewUserMessageWithBlocks(img))
	parts := msg.OfUser.Content.OfArrayOfContentParts
	if parts[0].OfImageURL.ImageURL.URL != "https://example.com/x.png" {
		t.Errorf("URL: %q", parts[0].OfImageURL.ImageURL.URL)
	}
}

func TestOpenAIConvertUserMessage_TextPlusImage(t *testing.T) {
	img := NewImageFromURL("https://example.com/x.png")
	msg := convertUserMessage(NewUserMessageWithBlocks(
		ContentBlock{Type: ContentTypeText, Text: "describe this"},
		img,
	))
	parts := msg.OfUser.Content.OfArrayOfContentParts
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	if parts[0].OfText == nil || parts[0].OfText.Text != "describe this" {
		t.Errorf("text part: %+v", parts[0])
	}
	if parts[1].OfImageURL == nil {
		t.Errorf("image part: %+v", parts[1])
	}
}

func TestOpenAIConvertUserMessage_PlainTextStillWorks(t *testing.T) {
	msg := convertUserMessage(NewUserMessage("hello"))
	if msg.OfUser == nil {
		t.Fatalf("expected user message, got %+v", msg)
	}
	if s := msg.OfUser.Content.OfString; !s.Valid() || s.Value != "hello" {
		t.Errorf("plain text should stay string form, got %+v", msg.OfUser.Content)
	}
}

func TestOpenAIConvertUserMessage_ToolResultStillWorks(t *testing.T) {
	msg := Message{
		Role:   RoleUser,
		Blocks: []ContentBlock{{Type: ContentTypeToolResult, ToolUseID: "t1", Text: "done"}},
	}
	converted := convertUserMessage(msg)
	if converted.OfTool == nil {
		t.Errorf("expected tool message, got %+v", converted)
	}
}
```

**Step 2: Run (fails)**

Run: `go test ./llm/... -run TestOpenAIConvertUserMessage -v`
Expected: the existing tool-result / plain-text tests still pass; new image tests fail (no multipart building yet).

**Step 3: Refactor `convertUserMessage` in `llm/openai.go`**

Replace the current `convertUserMessage` function with:

```go
func convertUserMessage(msg Message) openai.ChatCompletionMessageParamUnion {
	// Tool result still routes to a tool message (OpenAI's required shape).
	for _, block := range msg.Blocks {
		if block.Type == ContentTypeToolResult {
			return openai.ToolMessage(block.Text, block.ToolUseID)
		}
	}

	// Collect text + media parts.
	var parts []openai.ChatCompletionContentPartUnionParam
	if msg.Content != "" {
		parts = append(parts, openai.TextContentPart(msg.Content))
	}
	for _, block := range msg.Blocks {
		part, ok := convertOpenAIUserBlock(block)
		if ok {
			parts = append(parts, part)
		}
	}

	// If we only have a single plain-text part and no blocks, keep the simple
	// string form — avoids churning existing wire behavior for non-multimodal
	// callers.
	if len(parts) == 1 && len(msg.Blocks) == 0 {
		return openai.UserMessage(msg.Content)
	}

	if len(parts) == 0 {
		return openai.UserMessage("")
	}

	return openai.UserMessage(parts)
}

// convertOpenAIUserBlock converts a single user-message block to an OpenAI
// content part. Returns ok=false for blocks that don't translate (e.g.
// tool_use on user side, already-handled tool_result).
func convertOpenAIUserBlock(block ContentBlock) (openai.ChatCompletionContentPartUnionParam, bool) {
	switch block.Type {
	case ContentTypeText:
		return openai.TextContentPart(block.Text), true
	case ContentTypeImage:
		return convertOpenAIImage(block), true
	}
	return openai.ChatCompletionContentPartUnionParam{}, false
}

func convertOpenAIImage(block ContentBlock) openai.ChatCompletionContentPartUnionParam {
	if block.Source == nil {
		return openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{})
	}
	var url string
	switch block.Source.Kind {
	case SourceKindURL:
		url = block.Source.URL
	default:
		encoded := base64.StdEncoding.EncodeToString(block.Source.Bytes)
		url = "data:" + block.MediaType + ";base64," + encoded
	}
	return openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{URL: url})
}
```

Add `"encoding/base64"` to imports.

**Step 4: Run tests**

Run: `go test ./llm/... -run TestOpenAIConvert -v`
Expected: PASS all including the existing ones.

Run: `go test ./llm/...`
Expected: PASS.

**Step 5: Commit**

```bash
git add llm/openai.go llm/openai_test.go
git commit -m "feat(llm): OpenAI multipart user messages + image translation"
```

---

## Task 11: OpenAI PDF + audio translation, validation wiring, source-form errors

**Files:**
- Modify: `llm/openai.go`
- Modify: `llm/openai_test.go`

**Step 1: Write failing tests**

Append to `llm/openai_test.go`:

```go
func TestOpenAIConvertUserMessage_PDFBytes(t *testing.T) {
	pdf, err := NewPDFFromBytes([]byte("%PDF"))
	if err != nil {
		t.Fatal(err)
	}
	msg := convertUserMessage(NewUserMessageWithBlocks(pdf))
	parts := msg.OfUser.Content.OfArrayOfContentParts
	if parts[0].OfFile == nil {
		t.Fatalf("expected file part, got %+v", parts[0])
	}
	if !parts[0].OfFile.File.FileData.Valid() {
		t.Error("expected FileData set")
	}
	if !parts[0].OfFile.File.Filename.Valid() || parts[0].OfFile.File.Filename.Value != "file.pdf" {
		t.Errorf("default filename: %+v", parts[0].OfFile.File.Filename)
	}
}

func TestOpenAIConvertUserMessage_PDFFileKeepsFilename(t *testing.T) {
	pdf, err := NewPDFFromFile("testdata/tiny.pdf")
	if err != nil {
		t.Fatal(err)
	}
	msg := convertUserMessage(NewUserMessageWithBlocks(pdf))
	parts := msg.OfUser.Content.OfArrayOfContentParts
	if parts[0].OfFile.File.Filename.Value != "tiny.pdf" {
		t.Errorf("filename: %q", parts[0].OfFile.File.Filename.Value)
	}
}

func TestOpenAIConvertUserMessage_AudioMP3(t *testing.T) {
	audio, err := NewAudioFromBytes("audio/mpeg", []byte{0xff, 0xfb})
	if err != nil {
		t.Fatal(err)
	}
	msg := convertUserMessage(NewUserMessageWithBlocks(audio))
	parts := msg.OfUser.Content.OfArrayOfContentParts
	if parts[0].OfInputAudio == nil {
		t.Fatalf("expected audio part, got %+v", parts[0])
	}
	if parts[0].OfInputAudio.InputAudio.Format != "mp3" {
		t.Errorf("format: %q (audio/mpeg should map to mp3)", parts[0].OfInputAudio.InputAudio.Format)
	}
	if parts[0].OfInputAudio.InputAudio.Data == "" {
		t.Error("expected base64 data set")
	}
}

func TestOpenAIConvertUserMessage_AudioWAV(t *testing.T) {
	audio, err := NewAudioFromBytes("audio/wav", []byte{0x52, 0x49, 0x46, 0x46})
	if err != nil {
		t.Fatal(err)
	}
	msg := convertUserMessage(NewUserMessageWithBlocks(audio))
	parts := msg.OfUser.Content.OfArrayOfContentParts
	if parts[0].OfInputAudio.InputAudio.Format != "wav" {
		t.Errorf("format: %q", parts[0].OfInputAudio.InputAudio.Format)
	}
}

func TestOpenAICreateMessage_PDFFromURL_ErrUnsupportedSource(t *testing.T) {
	pdf := NewPDFFromURL("https://example.com/x.pdf")
	c := NewOpenAIClient("fake-key", "")
	_, err := c.CreateMessage(context.Background(), &Request{
		Messages: []Message{NewUserMessageWithBlocks(pdf)},
	})
	var ue *ErrUnsupportedSource
	if !errors.As(err, &ue) {
		t.Fatalf("expected *ErrUnsupportedSource, got %T: %v", err, err)
	}
	if ue.Provider != "openai" || ue.Media != "pdf" || ue.Kind != "url" {
		t.Errorf("err fields: %+v", ue)
	}
}

func TestOpenAICreateMessage_AudioFromURL_ErrUnsupportedSource(t *testing.T) {
	audio := NewAudioFromURL("https://example.com/a.mp3")
	c := NewOpenAIClient("fake-key", "")
	_, err := c.CreateMessage(context.Background(), &Request{
		Messages: []Message{NewUserMessageWithBlocks(audio)},
	})
	var ue *ErrUnsupportedSource
	if !errors.As(err, &ue) {
		t.Fatalf("expected *ErrUnsupportedSource, got %T: %v", err, err)
	}
	if ue.Media != "audio" || ue.Kind != "url" {
		t.Errorf("err fields: %+v", ue)
	}
}
```

**Step 2: Run (fails)**

Run: `go test ./llm/... -run TestOpenAI -v`
Expected: the six new tests fail.

**Step 3: Extend `convertOpenAIUserBlock` and add helpers**

In `llm/openai.go`, extend `convertOpenAIUserBlock` with PDF + audio cases. These helpers can return an error for unsupported source forms, which we'll propagate up.

Replace the previous `convertUserMessage` / `convertOpenAIUserBlock` skeleton with an error-returning variant. Put the full revised functions here:

```go
func convertUserMessage(msg Message) openai.ChatCompletionMessageParamUnion {
	// convertUserMessage itself can't return an error (existing signature).
	// Source-form errors are surfaced by convertOpenAIRequest, which runs
	// earlier inside CreateMessage. convertUserMessage therefore assumes
	// blocks are already source-form-valid.
	for _, block := range msg.Blocks {
		if block.Type == ContentTypeToolResult {
			return openai.ToolMessage(block.Text, block.ToolUseID)
		}
	}

	var parts []openai.ChatCompletionContentPartUnionParam
	if msg.Content != "" {
		parts = append(parts, openai.TextContentPart(msg.Content))
	}
	for _, block := range msg.Blocks {
		switch block.Type {
		case ContentTypeText:
			parts = append(parts, openai.TextContentPart(block.Text))
		case ContentTypeImage:
			parts = append(parts, convertOpenAIImage(block))
		case ContentTypePDF:
			parts = append(parts, convertOpenAIPDF(block))
		case ContentTypeAudio:
			parts = append(parts, convertOpenAIAudio(block))
		}
	}

	if len(parts) == 0 {
		return openai.UserMessage("")
	}
	// Preserve plain-string form for single-text-no-blocks callers.
	if len(parts) == 1 && len(msg.Blocks) == 0 {
		return openai.UserMessage(msg.Content)
	}
	return openai.UserMessage(parts)
}

func convertOpenAIPDF(block ContentBlock) openai.ChatCompletionContentPartUnionParam {
	// URL form is rejected pre-flight by validateOpenAISources — we only see
	// Bytes/File here.
	encoded := base64.StdEncoding.EncodeToString(block.Source.Bytes)
	filename := "file.pdf"
	if block.Source.Path != "" {
		filename = filepath.Base(block.Source.Path)
	}
	return openai.FileContentPart(openai.ChatCompletionContentPartFileFileParam{
		FileData: openai.String(encoded),
		Filename: openai.String(filename),
	})
}

func convertOpenAIAudio(block ContentBlock) openai.ChatCompletionContentPartUnionParam {
	format := openaiAudioFormat(block.MediaType)
	encoded := base64.StdEncoding.EncodeToString(block.Source.Bytes)
	return openai.InputAudioContentPart(openai.ChatCompletionContentPartInputAudioInputAudioParam{
		Data:   encoded,
		Format: format,
	})
}

// openaiAudioFormat maps a MIME type to OpenAI's input_audio format.
// audio/mpeg → "mp3"; audio/wav, audio/x-wav → "wav".
// Unknown types fall back to "mp3" because OpenAI validates this field;
// callers are expected to use a mapped MIME type in practice.
func openaiAudioFormat(mediaType string) string {
	switch mediaType {
	case "audio/wav", "audio/x-wav":
		return "wav"
	default:
		return "mp3"
	}
}

// validateOpenAISources checks every user-message block for source-form
// compatibility. Returns *ErrUnsupportedSource for PDF/audio via URL.
func validateOpenAISources(req *Request) error {
	for _, msg := range req.Messages {
		if msg.Role != RoleUser {
			continue
		}
		for _, block := range msg.Blocks {
			if block.Source == nil {
				continue
			}
			if block.Source.Kind != SourceKindURL {
				continue
			}
			switch block.Type {
			case ContentTypePDF:
				return &ErrUnsupportedSource{Provider: "openai", Media: "pdf", Kind: "url"}
			case ContentTypeAudio:
				return &ErrUnsupportedSource{Provider: "openai", Media: "audio", Kind: "url"}
			}
		}
	}
	return nil
}
```

Add `"path/filepath"` to imports.

**Step 4: Wire validation into `CreateMessage` and `CreateMessageStream`**

In both methods, add at the top (after the default-model-and-max-tokens block):

```go
if err := validateRequest("openai", o.Capabilities(), req); err != nil {
	return nil, err
}
if err := validateOpenAISources(req); err != nil {
	return nil, err
}
```

`CreateMessageStream` returns `(<-chan StreamEvent, error)`, so the same `return nil, err` pattern applies.

**Step 5: Run tests**

Run: `go test ./llm/... -run TestOpenAI -v`
Expected: PASS all including new source-form error tests.

Run: `go test ./...`
Expected: PASS.

**Step 6: Commit**

```bash
git add llm/openai.go llm/openai_test.go
git commit -m "feat(llm): OpenAI PDF/audio translation + ErrUnsupportedSource for URL variants"
```

---

## Task 12: Full-suite verification

**Step 1: Vet**

Run: `go vet ./...`
Expected: no output.

**Step 2: Build**

Run: `go build ./...`
Expected: no output.

**Step 3: Full test run**

Run: `go test ./...`
Expected: all packages `ok`, no failures.

**Step 4: Race detector (extra confidence, optional but recommended)**

Run: `go test -race ./llm/...`
Expected: PASS.

**Step 5: Run example to confirm it still builds**

Run: `go build ./examples/simple/`
Expected: no output.

**Step 6: If anything fails**, use superpowers:systematic-debugging before patching. Do not silence by changing test expectations.

If everything passes, the feature is ready for review.

---

## Out of scope (Phase 2, not this plan)

- **Gemini provider**: new `llm/gemini.go` using `google.golang.org/genai`, `Capabilities{Image: true, PDF: true, Audio: true, Video: true}`.
- **`ContentTypeVideo` and `NewVideoFromURL/Bytes/File`**: add only when the first video-capable provider lands. Adding a dead enum value now would be YAGNI.
- **Model-level capability maps**: deferred indefinitely; source-form errors + provider 400s cover current needs.

See `docs/plans/2026-04-21-llm-multimodal-input-design.md` for full Phase 2 notes.
