// ABOUTME: Tests for ContentBlock media extensions, MediaSource variants,
// ABOUTME: and JSON round-trip behavior for multimodal content.
package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	var restored ContentBlock
	_ = json.Unmarshal(data, &restored)
	if restored.Source != nil {
		t.Errorf("Source should be nil for text block, got %+v", restored.Source)
	}
}

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

func TestFullCapabilities(t *testing.T) {
	caps := FullCapabilities()
	if !caps.Image || !caps.PDF || !caps.Audio || !caps.Video {
		t.Errorf("FullCapabilities should enable everything: %+v", caps)
	}
}

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
	p := filepath.Join(dir, "weird.zzzfakeext")
	if err := os.WriteFile(p, []byte("blob"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := NewImageFromFile(p)
	if err == nil {
		t.Fatal("expected error for unknown extension")
	}
	if !strings.Contains(err.Error(), "could not infer") {
		t.Errorf("expected 'could not infer' error, got %v", err)
	}
}
