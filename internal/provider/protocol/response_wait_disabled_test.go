package protocol

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"hjh/internal/contracts"
)

func TestResponseWaitHTTPDisabledClock(t *testing.T) {
	for _, disabled := range []string{"start", "idle"} {
		t.Run(disabled, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if disabled == "idle" {
					_, _ = io.WriteString(w, ": heartbeat\n\n")
					w.(http.Flusher).Flush()
				}
				time.Sleep(100 * time.Millisecond)
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
			}))
			defer server.Close()
			config := ResponseTimeout{StartSeconds: seconds(0), IdleSeconds: seconds(.03)}
			if disabled == "idle" {
				config = ResponseTimeout{StartSeconds: seconds(.08), IdleSeconds: seconds(0)}
			}
			provider := New(Config{API: "chat-completions", BaseURL: server.URL, ResponseTimeout: config})
			if _, err := provider.Generate(context.Background(), contracts.ModelRequest{}, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
