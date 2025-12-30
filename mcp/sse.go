// ABOUTME: Implements SSE (Server-Sent Events) parsing for Streamable HTTP.
// ABOUTME: Parses event streams per the SSE specification.
package mcp

import (
	"bufio"
	"io"
	"strings"
)

// sseEvent represents a parsed SSE event.
type sseEvent struct {
	Event string
	Data  string
}

// sseReader reads SSE events one at a time from a stream.
type sseReader struct {
	scanner   *bufio.Scanner
	event     sseEvent
	dataLines []string
}

// newSSEReader creates a streaming SSE reader.
func newSSEReader(r io.Reader) *sseReader {
	return &sseReader{
		scanner: bufio.NewScanner(r),
	}
}

// Next returns the next SSE event, or io.EOF when done.
func (r *sseReader) Next() (*sseEvent, error) {
	r.event = sseEvent{}
	r.dataLines = nil

	for r.scanner.Scan() {
		line := r.scanner.Text()

		if line == "" {
			// Blank line = end of event
			if r.event.Event != "" || len(r.dataLines) > 0 {
				r.event.Data = strings.Join(r.dataLines, "\n")
				return &r.event, nil
			}
			continue
		}

		if strings.HasPrefix(line, "event:") {
			r.event.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			r.dataLines = append(r.dataLines, strings.TrimPrefix(line, "data: "))
		}
	}

	if err := r.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

// parseSSEEvents reads SSE events from a reader.
// Events are separated by blank lines.
func parseSSEEvents(r io.Reader) ([]sseEvent, error) {
	var events []sseEvent
	scanner := bufio.NewScanner(r)

	var currentEvent sseEvent
	var dataLines []string

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// Blank line = end of event
			if currentEvent.Event != "" || len(dataLines) > 0 {
				currentEvent.Data = strings.Join(dataLines, "\n")
				events = append(events, currentEvent)
				currentEvent = sseEvent{}
				dataLines = nil
			}
			continue
		}

		if strings.HasPrefix(line, "event:") {
			currentEvent.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		}
		// Ignore other fields (id:, retry:, comments)
	}

	return events, scanner.Err()
}
