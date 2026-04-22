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
	if !bytes.Equal(restored.Source.Bytes, original.Source.Bytes) {
		t.Errorf("Bytes mismatch: got %v want %v", restored.Source.Bytes, original.Source.Bytes)
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
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
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
		t.Errorf("bytes should have PNG magic, got %v", b.Source.Bytes)
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
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := NewPDFFromFile(p)
	if err == nil {
		t.Fatal("expected error for non-pdf extension")
	}
	if !strings.Contains(err.Error(), "application/pdf") {
		t.Errorf("expected error to mention application/pdf, got %v", err)
	}
}

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

func TestNewAudioFromBytes_RejectsEmpty(t *testing.T) {
	if _, err := NewAudioFromBytes("audio/mpeg", nil); err == nil {
		t.Fatal("expected error for empty bytes")
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

func TestValidateRequest_RejectsMissingSourceOnMediaBlock(t *testing.T) {
	caps := FullCapabilities()
	req := &Request{
		Messages: []Message{
			{Role: RoleUser, Blocks: []ContentBlock{{Type: ContentTypeImage}}},
		},
	}
	err := validateRequest("test", caps, req)
	var malformed *ErrMalformedMedia
	if !errors.As(err, &malformed) {
		t.Fatalf("expected *ErrMalformedMedia, got %T: %v", err, err)
	}
	if malformed.Provider != "test" || malformed.Media != "image" {
		t.Errorf("err fields: %+v", malformed)
	}
}

func TestNewImageFromFile_RejectsZeroByteFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "empty.png")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := NewImageFromFile(p)
	if err == nil {
		t.Fatal("expected error for zero-byte file")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected 'empty' error, got %v", err)
	}
}
