// Package webfetch retrieves web responses and turns bounded textual responses
// into readable text for the agent.
package webfetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	MaxBodyBytes = 64 * 1024
	timeout      = 30 * time.Second
	userAgent    = "hjh"
)

var (
	blockEnd   = regexp.MustCompile(`(?i)</(p|div|section|article|header|footer|li|tr|h[1-6]|blockquote|pre)>`)
	lineBreak  = regexp.MustCompile(`(?i)<br[ \t\r\n\f\v]*/?>`)
	tag        = regexp.MustCompile(`<[^>]+>`)
	space      = regexp.MustCompile(`[ \t\f\v]+`)
	newlines   = regexp.MustCompile(`\n{3,}`)
	scriptOpen = regexp.MustCompile(`(?i)<script\b[^>]*>`)
	scriptEnd  = regexp.MustCompile(`(?i)</script>`)
	styleOpen  = regexp.MustCompile(`(?i)<style\b[^>]*>`)
	styleEnd   = regexp.MustCompile(`(?i)</style>`)
)

// Fetch retrieves one HTTP(S) URL. When savePath is non-empty, it writes the
// original response bytes to that path without the ordinary body limit and
// returns download metadata instead of body text. displayPath is the path the
// caller supplied, while savePath is resolved by the host.
func Fetch(ctx context.Context, client *http.Client, rawURL, savePath, displayPath string) (string, error) {
	requestURL, err := parseURL(rawURL)
	if err != nil {
		return "", err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, requestURL, nil)
	if err != nil {
		return "", fmt.Errorf("web_fetch request failed: %w", err)
	}
	accept := "text/*, application/json, application/xml;q=0.9, */*;q=0.1"
	if savePath != "" {
		accept = "*/*"
	}
	request.Header.Set("Accept", accept)
	request.Header.Set("User-Agent", userAgent)

	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("web_fetch request failed: %w", err)
	}
	defer response.Body.Close()

	finalURL := responseURL(response, requestURL)
	contentType := response.Header.Get("Content-Type")
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if savePath == "" {
			return "", fmt.Errorf("web_fetch request failed (%d) for %s", response.StatusCode, finalURL)
		}
		return "", fmt.Errorf("web_fetch download failed for %s (%d) for %s", displayPath, response.StatusCode, finalURL)
	}

	if savePath != "" {
		if err := requestCtx.Err(); err != nil {
			return "", downloadError(displayPath, err)
		}
		file, err := os.OpenFile(savePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return "", downloadError(displayPath, err)
		}
		byteCount, copyErr := io.Copy(file, response.Body)
		closeErr := file.Close()
		if copyErr != nil {
			return "", downloadError(displayPath, copyErr)
		}
		if closeErr != nil {
			return "", downloadError(displayPath, closeErr)
		}
		return strings.Join([]string{
			"Saved: " + displayPath,
			"URL: " + finalURL,
			"Status: " + fmt.Sprint(response.StatusCode),
			"Content-Type: " + displayContentType(contentType),
			"Bytes: " + fmt.Sprint(byteCount),
		}, "\n"), nil
	}

	body, truncated, err := readBounded(response.Body)
	if err != nil {
		return "", fmt.Errorf("web_fetch request failed: %w", err)
	}
	textual := isTextualContentType(contentType)
	text := ""
	if textual {
		text = decodeUTF8(body)
		if strings.Contains(strings.ToLower(contentType), "text/html") {
			text = htmlToText(text)
		}
	}
	metadata := strings.Join([]string{
		"URL: " + finalURL,
		"Status: " + fmt.Sprint(response.StatusCode),
		"Content-Type: " + displayContentType(contentType),
		"truncated: " + fmt.Sprint(truncated),
	}, "\n")
	if !textual {
		return metadata, nil
	}
	result := metadata + "\n\n" + text
	if truncated {
		result += "\n\n[body truncated]"
	}
	return result, nil
}

func parseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("web_fetch URL must be an absolute http or https URL")
	}
	return parsed.String(), nil
}

func responseURL(response *http.Response, fallback string) string {
	if response.Request != nil && response.Request.URL != nil {
		return response.Request.URL.String()
	}
	return fallback
}

func displayContentType(contentType string) string {
	if contentType == "" {
		return "unknown"
	}
	return contentType
}

func downloadError(path string, err error) error {
	return fmt.Errorf("web_fetch download failed for %s: %w", path, err)
}

func readBounded(body io.Reader) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(body, MaxBodyBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) <= MaxBodyBytes {
		return data, false, nil
	}
	return data[:MaxBodyBytes], true, nil
}

func isTextualContentType(contentType string) bool {
	normalized := strings.ToLower(contentType)
	return strings.HasPrefix(normalized, "text/") ||
		strings.HasPrefix(normalized, "application/json") ||
		strings.HasPrefix(normalized, "application/xml") ||
		strings.HasPrefix(normalized, "application/xhtml+xml") ||
		strings.Contains(normalized, "+json") ||
		strings.Contains(normalized, "+xml")
}

func decodeUTF8(body []byte) string {
	text := strings.ToValidUTF8(string(body), "\uFFFD")
	return strings.TrimPrefix(text, "\uFEFF")
}

func htmlToText(html string) string {
	text := removeElementContent(html, scriptOpen, scriptEnd)
	text = removeElementContent(text, styleOpen, styleEnd)
	text = removeDelimited(text, "<!--", "-->")
	text = blockEnd.ReplaceAllString(text, "\n")
	text = lineBreak.ReplaceAllString(text, "\n")
	text = tag.ReplaceAllString(text, " ")
	text = decodeEntities(text)
	lines := strings.Split(text, "\n")
	cleaned := make([]string, 0, len(lines))
	for i, line := range lines {
		line = strings.TrimSpace(space.ReplaceAllString(line, " "))
		if line != "" || (i > 0 && strings.TrimSpace(space.ReplaceAllString(lines[i-1], " ")) != "") {
			cleaned = append(cleaned, line)
		}
	}
	return strings.TrimSpace(newlines.ReplaceAllString(strings.Join(cleaned, "\n"), "\n\n"))
}

func removeElementContent(input string, openPattern, closePattern *regexp.Regexp) string {
	var output strings.Builder
	for len(input) > 0 {
		start := openPattern.FindStringIndex(input)
		if start == nil {
			output.WriteString(input)
			break
		}
		output.WriteString(input[:start[0]])
		remainder := input[start[1]:]
		end := closePattern.FindStringIndex(remainder)
		if end == nil {
			output.WriteString(input[start[0]:])
			break
		}
		output.WriteByte(' ')
		input = remainder[end[1]:]
	}
	return output.String()
}

func removeDelimited(input, opening, closing string) string {
	for {
		start := strings.Index(input, opening)
		if start < 0 {
			return input
		}
		end := strings.Index(input[start+len(opening):], closing)
		if end < 0 {
			return input
		}
		end += start + len(opening) + len(closing)
		input = input[:start] + " " + input[end:]
	}
}

func decodeEntities(value string) string {
	return strings.NewReplacer(
		"&nbsp;", " ",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&#39;", "'",
		"&amp;", "&",
	).Replace(value)
}
