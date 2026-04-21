# LLM Multimodal Input Design

## Overview

Extend `llm.Client` to support multimodal input (images, PDFs, audio) on the
Anthropic and OpenAI providers. Output remains text + tool_use only — both
providers' chat APIs only emit text/tool calls. Video and a Gemini provider are
explicitly deferred to Phase 2.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Phase 1 scope | Images + PDFs + audio | Matches what current providers' chat APIs support |
| Sources | URL / Bytes / FilePath | Aligns with Rust port; covers caller ergonomics |
| File reads | Eager at construction | Errors land at the call site, not inside `CreateMessage` |
| Type shape | Extend flat `ContentBlock` | Matches existing pattern (tool_use/tool_result already share the struct) |
| Capability model | Provider-level static + `Capabilities()` query | Frontends can hide attachment UI for unsupported providers |
| Validation | Pre-flight typed errors | No wasted network round trips; deterministic UX |
| Phase 2 | Gemini provider + video | Documented as follow-on; type system avoids dead enum until then |

## Types

New `ContentType` values in `llm/types.go`:

```go
ContentTypeImage ContentType = "image"
ContentTypePDF   ContentType = "pdf"
ContentTypeAudio ContentType = "audio"
```

`MediaSource` — discriminated union for source form:

```go
type SourceKind string

const (
    SourceKindURL   SourceKind = "url"
    SourceKindBytes SourceKind = "bytes"
    SourceKindFile  SourceKind = "file"
)

type MediaSource struct {
    Kind  SourceKind `json:"kind"`
    URL   string     `json:"url,omitempty"`
    Bytes []byte     `json:"bytes,omitempty"`
    Path  string     `json:"path,omitempty"` // informational; bytes already populated for SourceKindFile
}
```

Two new fields on `ContentBlock`:

```go
Source    *MediaSource `json:"source,omitempty"`
MediaType string       `json:"media_type,omitempty"` // "image/png", "application/pdf", "audio/mpeg", ...
```

`Path` is retained after the eager read so providers/UIs can show a useful
filename and OpenAI's file part has a name to send.

## Constructors

All in `llm/types.go`. Constructors enforce valid construction so callers don't
poke fields directly.

```go
// Images
func NewImageFromURL(url string) ContentBlock
func NewImageFromBytes(mediaType string, data []byte) (ContentBlock, error)
func NewImageFromFile(path string) (ContentBlock, error)

// PDFs
func NewPDFFromURL(url string) ContentBlock
func NewPDFFromBytes(data []byte) (ContentBlock, error)
func NewPDFFromFile(path string) (ContentBlock, error)

// Audio
func NewAudioFromURL(url string) ContentBlock
func NewAudioFromBytes(mediaType string, data []byte) (ContentBlock, error)
func NewAudioFromFile(path string) (ContentBlock, error)

// Convenience
func NewUserMessageWithBlocks(blocks ...ContentBlock) Message
```

URL constructors don't return error — URLs are opaque to us; provider rejects
bad URLs at request time. File constructors read eagerly via `os.ReadFile`,
infer media type via `mime.TypeByExtension`, and validate the family
(image/_, application/pdf, audio/_) before returning.

## Capabilities

`Client` interface gains one method:

```go
type Capabilities struct {
    Image bool
    PDF   bool
    Audio bool
    Video bool
}

type Client interface {
    CreateMessage(ctx context.Context, req *Request) (*Response, error)
    CreateMessageStream(ctx context.Context, req *Request) (<-chan StreamEvent, error)
    Capabilities() Capabilities
}
```

Provider declarations:

| Provider  | Image | PDF  | Audio | Video |
|-----------|-------|------|-------|-------|
| Anthropic | true  | true | false | false |
| OpenAI    | true  | true | true  | false |

Test/example mocks use `llm.FullCapabilities()` helper to avoid bespoke values
per mock.

## Errors

Two distinct typed errors:

```go
type ErrUnsupportedMedia struct {
    Provider string
    Media    string
}

type ErrUnsupportedSource struct {
    Provider string
    Media    string
    Kind     string
}
```

Both implement `error` and are detectable via `errors.As`.

`ErrUnsupportedMedia` fires when a provider can't handle the media type at all
(e.g. audio on Anthropic). `Capabilities()` answers the same question
declaratively, for UI use.

`ErrUnsupportedSource` fires when the provider supports the media type but not
the source form (OpenAI's file part takes inline `file_data`/`file_id`, not
URL; OpenAI's `input_audio` takes inline data only). Source-form constraints
aren't reflected in `Capabilities` — they're narrower runtime errors at send.

## Validation

A new `validateRequest(caps Capabilities, req *Request) error` runs at the top
of each provider's `CreateMessage` and `CreateMessageStream`. It walks every
message's blocks and returns `ErrUnsupportedMedia` before any SDK call.
Source-form validation happens inline during translation in the provider's
`convertRequest`.

For streaming, validation errors are returned synchronously from the function
(not delivered as `EventError`) since they're pre-flight.

## Provider Translation

### Anthropic (`llm/anthropic.go`)

- Image: `anthropic.NewImageBlockBase64(mediaType, base64(bytes))` for inline;
  URL form via the SDK's URL source variant.
- PDF: `DocumentBlockParam` with `Source` switching on `SourceKind` the same
  way.
- Audio: never reached — `validateRequest` rejects.

### OpenAI (`llm/openai.go`)

User-message convert path becomes a multipart-aware builder (today it returns
a single text or tool message — extend to assemble
`[]ChatCompletionContentPartUnion`).

- Image: `image_url` part. URL → URL directly. Bytes → `data:<mediaType>;base64,<encoded>`.
- PDF: `ChatCompletionContentPartFileParam` with `file_data` (base64) +
  filename from `Source.Path` (else `"file.pdf"`). URL → `ErrUnsupportedSource`.
- Audio: `ChatCompletionContentPartInputAudioParam` with format inferred from
  media type (`audio/mpeg` → `mp3`, `audio/wav` → `wav`). URL →
  `ErrUnsupportedSource`.

## Backward Compatibility

The public `Client` interface gains `Capabilities()` — any external implementor
breaks at compile time. Acceptable: only consumers are in this repo, and the
compiler points at every fix. All in-repo mocks
(`orchestrator/orchestrator_test.go`, `agent/agent_test.go`, `llm/llm_test.go`,
`integration_test.go`, `examples/simple/main.go`) get a one-line
`Capabilities()` returning `llm.FullCapabilities()`.

The orchestrator needs no change — it appends `Blocks` to user messages today
(`orchestrator/orchestrator.go:73, 196`); multimodal blocks flow through
unchanged.

## Tests

- `llm/types_test.go` (new): constructor success/error paths — bad media type,
  missing file, unsupported extension, empty bytes. Round-trip JSON for
  `ContentBlock` with `Source`.
- `llm/anthropic_test.go`: extend `TestConvertRequest_ComplexMessageBlocks`
  with image (URL + bytes) and PDF (URL + bytes); add
  `TestAnthropicCapabilities` and
  `TestAnthropic_AudioReturnsErrUnsupportedMedia`.
- `llm/openai_test.go`: image (URL + bytes via data URL), PDF (bytes), audio
  (bytes, format inference for mp3/wav); `TestOpenAICapabilities`;
  `TestOpenAI_PDFFromURLReturnsErrUnsupportedSource`;
  `TestOpenAI_AudioFromURLReturnsErrUnsupportedSource`.
- `llm/llm_test.go`: assert mocks satisfy the extended interface.
- Test fixtures in `llm/testdata/`: synthetic PNG, minimal PDF
  (`%PDF-1.4\n%%EOF`), synthetic mp3 header. No real files, no network.

Translation layer is tested directly (input → SDK params), matching the
existing `TestConvertRequest_ComplexMessageBlocks` pattern.

## Phase 2 (deferred)

Not built in this work; documented so the type surface evolves cleanly:

- Add Gemini provider via `google.golang.org/genai` with
  `Capabilities{Image: true, PDF: true, Audio: true, Video: true}`.
- Introduce `ContentTypeVideo` and `NewVideoFromURL/Bytes/File` constructors at
  that time (not before — avoids a dead enum value).
- Anthropic and OpenAI capability declarations stay `Video: false`; their
  `validateRequest` rejects automatically.
- Source-form constraints for video on Gemini surface via `ErrUnsupportedSource`
  using the same machinery.

## Files Changed

- `llm/types.go` — types, constructors, validators, error types, capabilities.
- `llm/client.go` — `Capabilities()` on the interface.
- `llm/anthropic.go` — `Capabilities()`, validation, image/PDF translation.
- `llm/openai.go` — `Capabilities()`, validation, multipart user-message
  builder, image/PDF/audio translation.
- `llm/types_test.go` (new), `llm/anthropic_test.go`, `llm/openai_test.go`,
  `llm/llm_test.go` — coverage per Tests section.
- `llm/testdata/` (new) — synthetic fixtures.
- `orchestrator/orchestrator_test.go`, `agent/agent_test.go`,
  `integration_test.go`, `examples/simple/main.go` — `Capabilities()` on
  in-repo mocks.
