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
