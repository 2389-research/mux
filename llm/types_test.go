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
	var restored ContentBlock
	_ = json.Unmarshal(data, &restored)
	if restored.Source != nil {
		t.Errorf("Source should be nil for text block, got %+v", restored.Source)
	}
}
