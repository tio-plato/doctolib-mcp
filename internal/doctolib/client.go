// Package doctolib sends the anonymous requests to Doctolib's web JSON endpoints.
//
// Every request goes through the usage gate, carries the same browser headers,
// is sent once (no retry, no cookie, no redirect) and is classified as a
// success, a block or another error.
package doctolib

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tio-plato/doctolib-mcp/internal/gate"
)

// DefaultBaseURL is the French Doctolib site.
const DefaultBaseURL = "https://www.doctolib.fr"

// ChromeMajorEnv overrides the Chrome major version in the User-Agent.
const ChromeMajorEnv = "DOCTOLIB_MCP_CHROME_MAJOR"

// defaultChromeMajor is the Chrome major version in the User-Agent.
// Nix builds replace it with nixpkgs' chromium version through -ldflags -X.
var defaultChromeMajor = "157" //nolint:gochecknoglobals // set at link time with -ldflags -X

const (
	requestTimeout = 30 * time.Second
	maxBody        = 32 << 20
	markerTextLen  = 300
)

// ChromeMajor returns the Chrome major version for the User-Agent: $DOCTOLIB_MCP_CHROME_MAJOR if set, else the default.
func ChromeMajor() (string, error) {
	v := os.Getenv(ChromeMajorEnv)
	if v == "" {
		return defaultChromeMajor, nil
	}
	if !regexp.MustCompile(`^[0-9]{1,4}$`).MatchString(v) {
		return "", fmt.Errorf("%s=%q: want a Chrome major version such as 157", ChromeMajorEnv, v)
	}
	return v, nil
}

// Config sets up a [Client].
type Config struct {
	// BaseURL is the site root, without a trailing slash.
	BaseURL     string
	ChromeMajor string
	Gate        *gate.Gate
	// Timeout bounds each request; zero means 30 seconds.
	Timeout time.Duration
}

// Client sends requests to Doctolib.
type Client struct {
	baseURL   string
	userAgent string
	gate      *gate.Gate
	http      *http.Client
}

// New returns a client for c.BaseURL.
func New(c Config) *Client {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = requestTimeout
	}
	return &Client{
		baseURL: strings.TrimSuffix(c.BaseURL, "/"),
		userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" +
			c.ChromeMajor + ".0.0.0 Safari/537.36",
		gate: c.Gate,
		http: &http.Client{
			Timeout: timeout,
			// A redirect is reported as an error, never followed.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// BaseURL is the site root the client talks to.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// Gate is the usage gate every request goes through.
func (c *Client) Gate() *gate.Gate {
	return c.gate
}

// StatusError reports a response that is neither a success nor a block.
type StatusError struct {
	Path        string
	Status      int
	ContentType string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("Doctolib answered HTTP %d (%s) on %s", e.Status, e.ContentType, e.Path)
}

// Get sends a GET request; rawQuery is already encoded and keeps its order.
func (c *Client) Get(ctx context.Context, path, rawQuery string) ([]byte, error) {
	return c.do(ctx, http.MethodGet, path, rawQuery, nil)
}

// Post sends body as JSON in a POST request; rawQuery is already encoded.
func (c *Client) Post(ctx context.Context, path, rawQuery string, body any) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode request body for %s: %w", path, err)
	}
	return c.do(ctx, http.MethodPost, path, rawQuery, data)
}

func (c *Client) do(ctx context.Context, method, path, rawQuery string, body []byte) ([]byte, error) {
	target := c.baseURL + path
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", path, err)
	}
	c.setHeaders(req)

	err = c.gate.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("pass the usage gate: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("read the response to %s %s: %w", method, path, err)
	}

	contentType := resp.Header.Get("Content-Type")
	switch classify(resp.StatusCode, contentType) {
	case blocked:
		m, blockErr := c.gate.Block(path, resp.StatusCode, visibleText(data, contentType))
		return nil, &gate.BlockedError{Marker: m, MarkerPath: c.gate.MarkerPath(), WriteErr: blockErr}
	case success:
		return data, nil
	case failure:
	}
	return nil, &StatusError{Path: path, Status: resp.StatusCode, ContentType: contentType}
}

// setHeaders gives every request, whatever its endpoint or method, the headers of the Doctolib web front end.
func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "fr-FR,fr;q=0.9")
	req.Header.Set("Referer", c.baseURL+"/")
	if req.Method == http.MethodPost {
		req.Header.Set("Origin", c.baseURL)
		req.Header.Set("Content-Type", "application/json")
	}
}

type outcome int

const (
	success outcome = iota
	blocked
	failure
)

// classify sorts a response; the first rule that matches wins.
//
//  1. 403 and 429 are blocks, whatever the body.
//  2. HTML with a 2xx or a 503 is a block: Cloudflare challenges are HTML.
//  3. JSON with a 2xx (206 included) is a success.
//  4. Anything else is an error, not a block: a stale link or an outage
//     must not leave a marker only a person can clear.
func classify(status int, contentType string) outcome {
	media, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		media = ""
	}
	ok := status >= http.StatusOK && status < http.StatusMultipleChoices
	isHTML := media == "text/html" || media == "application/xhtml+xml"
	isJSON := media == "application/json" || strings.HasSuffix(media, "+json")
	switch {
	case status == http.StatusForbidden || status == http.StatusTooManyRequests:
		return blocked
	case isHTML && (ok || status == http.StatusServiceUnavailable):
		return blocked
	case ok && isJSON:
		return success
	default:
		return failure
	}
}

var (
	hiddenHTML = regexp.MustCompile(`(?is)<(script|style|noscript)\b.*?</(script|style|noscript)>`)
	htmlTag    = regexp.MustCompile(`(?s)<[^>]*>`)
)

// visibleText is the start of what a person sees on the response page; for a block page it holds the support code.
func visibleText(body []byte, contentType string) string {
	text := string(body)
	if strings.Contains(contentType, "html") || strings.HasPrefix(strings.TrimSpace(text), "<") {
		text = hiddenHTML.ReplaceAllString(text, " ")
		text = htmlTag.ReplaceAllString(text, " ")
		text = html.UnescapeString(text)
	}
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) > markerTextLen {
		text = string([]rune(text)[:markerTextLen])
	}
	return text
}
