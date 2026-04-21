// ABOUTME: Pre-flight validation that checks a Request's content blocks
// ABOUTME: against a provider's Capabilities before any network call.
package llm

// validateRequest returns *ErrUnsupportedMedia if any message block uses a
// media type the provider can't handle. Non-media blocks (text, tool_use,
// tool_result) are ignored. Source-form constraints are checked separately
// inside each provider's convert path (see Tasks 9/11).
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
