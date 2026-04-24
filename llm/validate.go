// ABOUTME: Pre-flight validation that checks a Request's content blocks
// ABOUTME: against a provider's Capabilities before any network call.
package llm

// validateRequest returns *ErrUnsupportedMedia if any message block uses a
// media type the provider can't handle, or *ErrMalformedMedia if a media
// block has no Source attached. Non-media blocks (text, tool_use,
// tool_result) are ignored. Source-form constraints (e.g. OpenAI rejecting
// URL-form PDFs) are checked separately inside each provider's convert path.
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
	var media string
	var supported bool
	switch block.Type {
	case ContentTypeImage:
		media, supported = "image", caps.Image
	case ContentTypePDF:
		media, supported = "pdf", caps.PDF
	case ContentTypeAudio:
		media, supported = "audio", caps.Audio
	case ContentTypeVideo:
		media, supported = "video", caps.Video
	default:
		return nil
	}
	if !supported {
		return &ErrUnsupportedMedia{Provider: provider, Media: media}
	}
	if block.Source == nil {
		return &ErrMalformedMedia{Provider: provider, Media: media, Reason: "missing Source"}
	}
	switch block.Source.Kind {
	case SourceKindURL:
		if block.Source.URL == "" {
			return &ErrMalformedMedia{Provider: provider, Media: media, Reason: "empty URL"}
		}
	case SourceKindBytes, SourceKindFile:
		if len(block.Source.Bytes) == 0 {
			return &ErrMalformedMedia{Provider: provider, Media: media, Reason: "empty data"}
		}
	default:
		return &ErrMalformedMedia{Provider: provider, Media: media, Reason: "unknown Source.Kind"}
	}
	return nil
}
