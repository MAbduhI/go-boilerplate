package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// SSEEvent represents a single Server-Sent Event frame.
type SSEEvent struct {
	ID    string `json:"id,omitempty"`
	Event string `json:"event,omitempty"`
	Data  any    `json:"data"`
	Retry int    `json:"retry,omitempty"`
}

// SSEStream represents an active SSE connection helper.
type SSEStream struct {
	c       *gin.Context
	w       http.ResponseWriter
	flusher http.Flusher
}

// NewSSEStream sets required headers and initializes the SSE response writer.
func NewSSEStream(c *gin.Context) (*SSEStream, error) {
	w := c.Writer
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("httpx: streaming unsupported by response writer")
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable Nginx proxy buffering

	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	return &SSEStream{
		c:       c,
		w:       w,
		flusher: flusher,
	}, nil
}

// Send emits a structured event frame to the client.
func (s *SSEStream) Send(event SSEEvent) error {
	var payload []byte
	switch d := event.Data.(type) {
	case []byte:
		payload = d
	case string:
		payload = []byte(d)
	default:
		var err error
		payload, err = json.Marshal(d)
		if err != nil {
			return fmt.Errorf("httpx: failed to marshal sse data: %w", err)
		}
	}

	if event.ID != "" {
		if _, err := fmt.Fprintf(s.w, "id: %s\n", event.ID); err != nil {
			return err
		}
	}
	if event.Event != "" {
		if _, err := fmt.Fprintf(s.w, "event: %s\n", event.Event); err != nil {
			return err
		}
	}
	if event.Retry > 0 {
		if _, err := fmt.Fprintf(s.w, "retry: %d\n", event.Retry); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", payload); err != nil {
		return err
	}

	s.flusher.Flush()
	return nil
}

// Heartbeat runs a periodic SSE comment ping (: keepalive) until ctx is done.
func (s *SSEStream) Heartbeat(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := io.WriteString(s.w, ": keepalive\n\n"); err != nil {
				return
			}
			s.flusher.Flush()
		}
	}
}
