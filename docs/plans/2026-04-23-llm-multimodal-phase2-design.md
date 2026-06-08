# LLM Multimodal Phase 2 Design

## Overview

Finish what PR #7 deferred. Wire real multimodal translation into the three
providers PR #7 stubbed with empty `Capabilities{}` (Gemini, Ollama,
OpenRouter), and add `ContentTypeVideo` plus `NewVideoFrom*` constructors so
the type system can carry video blocks now that Gemini actually consumes them.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Branch strategy | Stacked PR off `feat/llm-multimodal-input` | Unblocks Phase 2 immediately; GitHub auto-retargets to `main` once PR #7 merges |
| Gemini capabilities | `{Image: true, PDF: true, Audio: true, Video: true}` | Gemini natively supports all four via `inline_data` parts |
| Ollama capabilities | `{Image: true}` | Vision-capable models (LLaVA, Llama 3.2 Vision) accept image_url; no PDF/audio/video on chat endpoint |
| OpenRouter capabilities | `{Image: true, PDF: true, Audio: true}` | Mirrors OpenAI's matrix; routes upstream so model-level constraints fall through to provider 400 |
| URL form on Gemini | Reject pre-flight with `ErrUnsupportedSource` | Gemini's `inline_data` takes raw bytes; no URL fetch in Phase 2 (SSRF tradeoffs to be discussed separately) |
| Video URL constructor | Provided for symmetry; provider-rejected at translate-time | Matches OpenAI PDF/audio URL pattern |
| Ollama image enforcement | Centralized `validateRequest` only | Go's `checkBlock` already handles per-media rejection; no need for a Rust-style provider-level helper |

## Type System Additions

`llm/types.go`:

- `ContentTypeVideo ContentType = "video"` in the existing const block.
- `NewVideoFromURL(url string) ContentBlock` — sets Type+Source{URL}; MediaType empty.
- `NewVideoFromBytes(mediaType string, data []byte) (ContentBlock, error)` —
  validates `video/*` family + non-empty.
- `NewVideoFromFile(path string) (ContentBlock, error)` — extension-before-read
  via `readMediaFile`, validates `video/*`, basename in `Source.Path`.
- `validateMediaFamily` gains the `"video"` family (prefix-matches `video/`).

`llm/validate.go`:

- `checkBlock` gains a `case ContentTypeVideo` mirroring image/PDF/audio
  (capability check + Source-shape check).

`Capabilities.Video` and `FullCapabilities()` already exist from PR #7. No
changes there.

## Gemini Translation

`llm/gemini.go`:

```go
case ContentTypeImage, ContentTypePDF, ContentTypeAudio, ContentTypeVideo:
    if part := convertGeminiMedia(block); part != nil {
        parts = append(parts, part)
    }
```

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

`Capabilities()` flips to `{Image: true, PDF: true, Audio: true, Video: true}`.
Doc updates to reflect that translation now exists and that URL-form is
rejected because we don't fetch.

`validateGeminiSources(req)` is wired into both `CreateMessage` and
`CreateMessageStream` immediately after the existing `validateRequest` call.

## Ollama Translation

`llm/ollama.go` change is one function:

```go
func (o *OllamaClient) Capabilities() Capabilities {
    return Capabilities{Image: true}
}
```

That's the whole change. Image blocks flow through `convertOpenAIRequest` →
`convertUserMessage` → `convertOpenAIImage` (all from PR #7). PDF/audio/video
blocks are caught by the existing `validateRequest("ollama", o.Capabilities(),
req)` call and return `ErrUnsupportedMedia`.

## OpenRouter Translation

`llm/openrouter.go` change is also one function:

```go
func (o *OpenRouterClient) Capabilities() Capabilities {
    return Capabilities{Image: true, PDF: true, Audio: true}
}
```

Image/PDF/audio translation reuses the OpenAI multipart conversion path that
PR #7 already wired into OpenRouter's entry points. Video blocks rejected by
`validateRequest`. PDF/audio URL-form rejected by `validateOpenAISources`
(also wired in PR #7).

## Tests

### `llm/types_test.go`
- `TestNewVideoFromURL` — Type+Source.Kind+empty MediaType.
- `TestNewVideoFromBytes_OK` — `video/mp4` + bytes.
- `TestNewVideoFromBytes_RejectsNonVideo` — error mentions `video/`.
- `TestNewVideoFromBytes_RejectsEmpty` — nil bytes errors.
- `TestNewVideoFromFile_OK` — load `testdata/tiny.mp4`, assert MediaType +
  basename Path.
- `TestNewVideoFromFile_WrongExtension` — `.png` for video errors.
- Extend `TestValidateRequest_RejectsMalformedSourceVariants` with a Video
  sub-case.

### `llm/gemini_test.go`
- `TestGeminiCapabilities` — all four true.
- `TestGeminiConvertRequest_ImageBytes` / `_PDFBytes` / `_AudioBytes` /
  `_VideoBytes` — assert `parts[0].InlineData.MIMEType` + non-empty Data.
- `TestGeminiCreateMessage_ImageURL_ErrUnsupportedSource` — URL form rejected.
- `TestGeminiCreateMessage_VideoURL_ErrUnsupportedSource` — same for video.
- Replace `TestGeminiCreateMessage_RejectsImageMedia` (now stale).

### `llm/ollama_test.go`
- `TestOllamaCapabilities` — Image true; others false.
- `TestOllamaConvertRequest_ImageBytes` — image_url with data URL prefix.
- `TestOllamaCreateMessage_RejectsPDFMedia` — PDF block rejected.
- Replace `TestOllamaCreateMessage_RejectsImageMedia` (image now allowed).

### `llm/openrouter_test.go`
- `TestOpenRouterCapabilities` — Image+PDF+Audio true; Video false.
- `TestOpenRouterConvertRequest_ImageBytes` — image translation.
- `TestOpenRouterCreateMessage_VideoBytes_ErrUnsupportedMedia` — video
  rejected.
- `TestOpenRouterCreateMessage_PDFFromURL_ErrUnsupportedSource` — same as
  OpenAI URL-PDF rejection.
- Replace `TestOpenRouterCreateMessage_RejectsImageMedia` (image now allowed).

### Wire-format snapshot tests
Add gjson-based wire snapshots for Gemini (parallel to PR #7's
Anthropic/OpenAI snapshots): one per media kind asserting
`parts.0.inlineData.mimeType` and `parts.0.inlineData.data`.

### Fixtures
- `llm/testdata/tiny.mp4` — small synthetic blob, enough for
  `mime.TypeByExtension(".mp4")` to resolve to `video/mp4`. Same pattern as
  PR #7's tiny PNG/PDF/MP3.

## Out of Scope (Phase 3 candidates)

- **URL auto-fetch** — would let Gemini accept `NewVideoFromURL` end-to-end
  via internal HTTP fetching. SSRF tradeoffs need discussion; Rust port
  ships this but Go intentionally defers.
- **MCP tool-result image passthrough** — separate ergonomic improvement.
- **Per-model capability maps** — provider-level static + provider 400
  fall-through still wins for now.

## Files Changed

- `llm/types.go` — `ContentTypeVideo`, three Video constructors,
  `validateMediaFamily` extension.
- `llm/validate.go` — `checkBlock` gains Video case.
- `llm/gemini.go` — `convertGeminiMedia`, `validateGeminiSources`,
  switch extension, `Capabilities()` flip + doc, validation wiring.
- `llm/ollama.go` — `Capabilities()` flip + doc.
- `llm/openrouter.go` — `Capabilities()` flip + doc.
- `llm/types_test.go`, `llm/gemini_test.go`, `llm/ollama_test.go`,
  `llm/openrouter_test.go` — coverage per Tests section.
- `llm/testdata/tiny.mp4` (new).
