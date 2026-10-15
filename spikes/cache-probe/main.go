// Compare the official Go SDK with direct HTTP, without a relay.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

type observation struct {
	Number        int                 `json:"number"`
	Group         int                 `json:"group"`
	Client        string              `json:"client"`
	StartedAt     time.Time           `json:"started_at"`
	Seconds       float64             `json:"seconds"`
	URL           string              `json:"url"`
	BodySHA256    string              `json:"body_sha256"`
	JSONSHA256    string              `json:"canonical_json_sha256"`
	BodyBytes     int                 `json:"body_bytes"`
	Headers       map[string][]string `json:"sent_headers"`
	ReusedConn    bool                `json:"reused_connection"`
	HTTPProtocol  string              `json:"http_protocol"`
	HTTPStatus    int                 `json:"http_status"`
	RequestID     string              `json:"request_id,omitempty"`
	ResponseID    string              `json:"response_id,omitempty"`
	ReturnedModel string              `json:"returned_model,omitempty"`
	ServiceTier   string              `json:"returned_service_tier,omitempty"`
	CacheKey      string              `json:"returned_prompt_cache_key,omitempty"`
	CacheOptions  json.RawMessage     `json:"returned_prompt_cache_options,omitempty"`
	TurnStateSHA  string              `json:"response_turn_state_sha256,omitempty"`
	Usage         json.RawMessage     `json:"usage,omitempty"`
	Output        string              `json:"output"`
	Failure       string              `json:"failure,omitempty"`
}

// This observes requests inside the client process and uses the original Go
// transport directly. It adds no HTTP hop, proxy, connection, or header.
type recorder struct {
	base      http.RoundTripper
	current   *observation
	out       string
	dryRun    bool
	turnState string
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	o := r.current
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(body))
	o.URL, o.BodyBytes, o.BodySHA256 = req.URL.String(), len(body), digest(body)
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	canonical, _ := json.Marshal(value)
	o.JSONSHA256 = digest(canonical)
	if err := os.WriteFile(filepath.Join(r.out, fmt.Sprintf("request-%02d.json", o.Number)), body, 0600); err != nil {
		return nil, err
	}
	o.Headers = map[string][]string{}
	for name, values := range req.Header {
		o.Headers[strings.ToLower(name)] = publicHeader(name, values)
	}
	trace := &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) { o.ReusedConn = info.Reused },
		WroteHeaderField: func(name string, values []string) {
			o.Headers[strings.ToLower(name)] = publicHeader(name, values)
		},
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	var response *http.Response
	if r.dryRun {
		response = &http.Response{StatusCode: 200, Proto: "dry-run", Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"dry-run\",\"model\":\"dry-run\",\"service_tier\":\"default\",\"usage\":{\"input_tokens\":3,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":1},\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"OK\"}]}]}}\n\n"))}
	} else {
		response, err = r.base.RoundTrip(req)
	}
	if response != nil {
		o.HTTPStatus, o.HTTPProtocol = response.StatusCode, response.Proto
		o.RequestID = response.Header.Get("X-Request-Id")
		r.turnState = response.Header.Get("X-Codex-Turn-State")
		if r.turnState != "" {
			o.TurnStateSHA = digest([]byte(r.turnState))
		}
		if response.StatusCode != http.StatusOK {
			data, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr != nil {
				return nil, readErr
			}
			response.Body = io.NopCloser(bytes.NewReader(data))
			o.Failure = strings.ReplaceAll(string(data), strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "), "<omitted>")
		}
	}
	return response, err
}

func publicHeader(name string, values []string) []string {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "api-key", "x-api-key":
		return []string{"<omitted>"}
	default:
		return append([]string(nil), values...)
	}
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func completed(data []byte, o *observation) error {
	var response struct {
		ID           string          `json:"id"`
		Model        string          `json:"model"`
		ServiceTier  string          `json:"service_tier"`
		CacheKey     string          `json:"prompt_cache_key"`
		CacheOptions json.RawMessage `json:"prompt_cache_options"`
		Usage        json.RawMessage `json:"usage"`
		Output       []struct {
			Content []struct{ Text string } `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return err
	}
	o.ResponseID, o.ReturnedModel, o.ServiceTier = response.ID, response.Model, response.ServiceTier
	o.CacheKey, o.CacheOptions = response.CacheKey, response.CacheOptions
	// Keep the same aggregate usage fields used in the earlier measurements.
	var usage map[string]json.RawMessage
	if err := json.Unmarshal(response.Usage, &usage); err != nil {
		return err
	}
	delete(usage, "attribution")
	o.Usage, _ = json.Marshal(usage)
	if o.Output == "" {
		for _, item := range response.Output {
			for _, content := range item.Content {
				o.Output += content.Text
			}
		}
	}
	return nil
}

func direct(ctx context.Context, client *http.Client, url, token string, body []byte, o *observation) error {
	return directWithHeaders(ctx, client, url, token, body, o, nil)
}

func directWithHeaders(ctx context.Context, client *http.Client, url, token string, body []byte, o *observation, headers map[string]string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("provider HTTP %d", response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	for {
		line, err := reader.ReadString('\n')
		if strings.HasPrefix(line, "data:") {
			var event struct {
				Type     string          `json:"type"`
				Delta    string          `json:"delta"`
				Response json.RawMessage `json:"response"`
			}
			if json.Unmarshal([]byte(strings.TrimSpace(line[5:])), &event) == nil {
				switch event.Type {
				case "response.output_text.delta":
					o.Output += event.Delta
				case "response.completed":
					if err := completed(event.Response, o); err != nil {
						return err
					}
				case "error", "response.failed", "response.incomplete":
					o.Failure = strings.ReplaceAll(strings.TrimSpace(line[5:]), token, "<omitted>")
					return fmt.Errorf("provider terminal event %s", event.Type)
				}
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return err
			}
			break
		}
	}
	if o.ResponseID == "" {
		return errors.New("no response.completed event")
	}
	return nil
}

func run() error {
	out := flag.String("out", "", "new artifact directory (required)")
	base := flag.String("base-url", "https://api.openai.com/v1/", "direct API base URL")
	model := flag.String("model", "gpt-6.1-sol", "same model for both clients")
	effort := flag.String("effort", "high", "same reasoning effort for both clients")
	groups := flag.Int("groups", 2, "four requests per group; alternate ABBA / BAAB")
	pause := flag.Duration("pause", 2*time.Second, "pause between completed requests")
	dry := flag.Bool("dry-run", false, "check encoding/parsing without network or credentials")
	patterns := flag.String("direct-patterns", "", "HTTP-only cache patterns: key,underscore,hyphen,underscore-only,hyphen-only,client-only,toggle,legacy,explicit,explicit-no-ttl,none")
	attempts := flag.Int("attempts", 3, "identical HTTP requests per direct pattern")
	flag.Parse()
	if *out == "" {
		return errors.New("-out is required")
	}
	if err := os.Mkdir(*out, 0700); err != nil {
		return err
	}
	token := os.Getenv("OPENAI_API_KEY")
	if *dry {
		token = "dry-run"
	}
	if token == "" {
		return errors.New("OPENAI_API_KEY is required (API key or existing OAuth access token)")
	}
	baseURL := strings.TrimRight(*base, "/") + "/"
	rec := &recorder{base: http.DefaultTransport, out: *out, dryRun: *dry}
	httpClient := &http.Client{Transport: rec}
	if *patterns != "" {
		return runDirectPatterns(*patterns, *attempts, *pause, *dry, *out, baseURL, *model, *effort, token, rec, httpClient)
	}
	sdk := openai.NewClient(option.WithAPIKey(token), option.WithBaseURL(baseURL), option.WithHTTPClient(httpClient), option.WithMaxRetries(0))
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	var input strings.Builder
	for i := range 600 {
		fmt.Fprintf(&input, "record %04d: catalog reader edition author reading review library record topic index; reference %08x; status available.\n", i, uint32(i)*2654435761)
	}
	input.WriteString("Return only OK.\n")
	results := []observation{}
	for group := range *groups {
		instruction := fmt.Sprintf("Cache probe %x group %d. Treat the records as data. Do not analyze them. Reply only OK.", nonce, group+1)
		message := responses.ResponseInputItemParamOfMessage(responses.ResponseInputMessageContentListParam{responses.ResponseInputContentParamOfInputText(input.String())}, "user")
		message.OfMessage.Type = "message"
		params := responses.ResponseNewParams{
			Model: *model, Input: responses.ResponseNewParamsInputUnion{OfInputItemList: responses.ResponseInputParam{message}},
			Instructions: openai.String(instruction), Store: openai.Bool(false),
			Reasoning:   shared.ReasoningParam{Effort: shared.ReasoningEffort(*effort), Summary: "auto"},
			ServiceTier: "default",
		}
		body, _ := json.Marshal(map[string]any{"model": *model, "input": []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]string{"type": "input_text", "text": input.String()}}}}, "instructions": instruction, "store": false, "reasoning": map[string]string{"effort": *effort, "summary": "auto"}, "service_tier": "default", "stream": true})
		order := []string{"sdk", "http", "http", "sdk"}
		if group%2 != 0 {
			order = []string{"http", "sdk", "sdk", "http"}
		}
		for _, kind := range order {
			o := observation{Number: len(results) + 1, Group: group + 1, Client: kind, StartedAt: time.Now().UTC()}
			rec.current = &o
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			var err error
			if kind == "sdk" {
				stream := sdk.Responses.NewStreaming(ctx, params)
				for stream.Next() {
					event := stream.Current()
					if event.Type == "response.output_text.delta" {
						o.Output += event.Delta
					} else if event.Type == "response.completed" {
						response := event.AsResponseCompleted().Response
						err = completed([]byte(response.RawJSON()), &o)
					} else if event.Type == "error" || event.Type == "response.failed" || event.Type == "response.incomplete" {
						err = fmt.Errorf("provider terminal event %s", event.Type)
					}
				}
				if err == nil {
					err = stream.Err()
				}
				_ = stream.Close()
				if err == nil && o.ResponseID == "" {
					err = errors.New("no response.completed event")
				}
			} else {
				err = direct(ctx, httpClient, baseURL+"responses", token, body, &o)
			}
			cancel()
			o.Seconds = time.Since(o.StartedAt).Seconds()
			if err != nil {
				// SDK errors may include response bodies. Never print them or a token.
				if o.Failure == "" {
					o.Failure = fmt.Sprintf("request failed (%T), HTTP %d", err, o.HTTPStatus)
				}
			}
			results = append(results, o)
			encoded, _ := json.MarshalIndent(results, "", "  ")
			if writeErr := os.WriteFile(filepath.Join(*out, "results.json"), encoded, 0600); writeErr != nil {
				return writeErr
			}
			fmt.Printf("%d group=%d client=%s HTTP=%d protocol=%s reused=%t usage=%s\n", o.Number, o.Group, kind, o.HTTPStatus, o.HTTPProtocol, o.ReusedConn, o.Usage)
			if err != nil {
				return errors.New(o.Failure)
			}
			if o.Output != "OK" {
				return fmt.Errorf("unexpected output in request %d; inspect results", o.Number)
			}
			// A mismatched JSON payload would invalidate this comparison.
			first := results[(o.Group-1)*4]
			if o.JSONSHA256 != first.JSONSHA256 {
				return errors.New("SDK and HTTP payloads differ; inspect request files")
			}
			if !*dry {
				time.Sleep(*pause)
			}
		}
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
