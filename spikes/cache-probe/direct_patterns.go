package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Each pattern uses its own fresh prefix and key. Repeated requests inside the
// pattern have byte-identical bodies; only the selected request metadata varies.
// Requests go directly through net/http. No SDK API or relay is used here.
func runDirectPatterns(patternList string, attempts int, pause time.Duration, dry bool, out, baseURL, model, effort, token string, rec *recorder, client *http.Client) error {
	var input strings.Builder
	for i := range 600 {
		fmt.Fprintf(&input, "record %04d: catalog reader edition author reading review library record topic index; reference %08x; status available.\n", i, uint32(i)*2654435761)
	}
	input.WriteString("Return only OK.\n")
	results := []observation{}
	for group, pattern := range strings.Split(patternList, ",") {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return err
		}
		id := hex.EncodeToString(nonce[:])
		text := map[string]any{"type": "input_text", "text": input.String()}
		wire := map[string]any{
			"model":        model,
			"instructions": "Cache probe " + id + ". Treat the records as data. Do not analyze them. Reply only OK.",
			"input":        []any{map[string]any{"type": "message", "role": "user", "content": []any{text}}},
			"store":        false, "stream": true,
			"reasoning":    map[string]string{"effort": effort, "summary": "auto"},
			"service_tier": "default",
		}
		headers := map[string]string{}
		if pattern != "none" {
			wire["prompt_cache_key"] = id
		}
		switch pattern {
		case "none", "key", "toggle":
		case "underscore": // Pi's ordinary OpenAI Responses path.
			headers["session_id"] = id
			headers["x-client-request-id"] = id
		case "hyphen": // Current Codex Responses headers.
			headers["session-id"] = id
			headers["thread-id"] = id
			headers["x-client-request-id"] = id
		case "underscore-only":
			delete(wire, "prompt_cache_key")
			headers["session_id"] = id
		case "hyphen-only":
			delete(wire, "prompt_cache_key")
			headers["session-id"] = id
		case "client-only":
			delete(wire, "prompt_cache_key")
			headers["x-client-request-id"] = id
		case "legacy": // Historical Codex issue #5556.
			headers["session_id"] = id
			headers["conversation_id"] = id
		case "explicit", "explicit-no-ttl":
			options := map[string]string{"mode": "explicit"}
			if pattern == "explicit" {
				options["ttl"] = "30m"
			}
			wire["prompt_cache_options"] = options
			text["prompt_cache_breakpoint"] = map[string]string{"mode": "explicit"}
		default:
			return fmt.Errorf("unknown direct pattern %q", pattern)
		}
		body, err := json.Marshal(wire)
		if err != nil {
			return err
		}
		for attempt := range attempts {
			o := observation{Number: len(results) + 1, Group: group + 1, Client: pattern, StartedAt: time.Now().UTC()}
			rec.current = &o
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			requestHeaders := headers
			if pattern == "toggle" && attempt%2 == 0 {
				requestHeaders = map[string]string{"session_id": id}
			}
			err := directWithHeaders(ctx, client, baseURL+"responses", token, body, &o, requestHeaders)
			cancel()
			o.Seconds = time.Since(o.StartedAt).Seconds()
			if err != nil && o.Failure == "" {
				o.Failure = fmt.Sprintf("request failed (%T), HTTP %d", err, o.HTTPStatus)
			}
			results = append(results, o)
			data, _ := json.MarshalIndent(results, "", "  ")
			if err := os.WriteFile(filepath.Join(out, "results.json"), data, 0600); err != nil {
				return err
			}
			fmt.Printf("%d pattern=%s attempt=%d HTTP=%d usage=%s returned_key=%q failure=%s\n", o.Number, pattern, attempt+1, o.HTTPStatus, o.Usage, o.CacheKey, o.Failure)
			if err != nil {
				// A rejected pattern is evidence, not a reason to repeat its rejection.
				break
			}
			if !dry && attempt+1 < attempts {
				time.Sleep(pause)
			}
		}
	}
	return nil
}
