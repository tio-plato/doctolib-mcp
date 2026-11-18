// Package doctolib sends requests to the JSON endpoints behind Doctolib's website.
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

const DefaultBaseURL = "https://www.doctolib.fr"

const ChromeMajorEnv = "DOCTOLIB_MCP_CHROME_MAJOR"

var defaultChromeMajor = "157" //nolint:gochecknoglobals // flake.nix sets nixpkgs' chromium version with -ldflags -X

const (
	requestTimeout = 30 * time.Second
	maxBody        = 32 << 20
	markerTextLen  = 300
)

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

type Config struct {
	BaseURL     string
	ChromeMajor string
	Gate        *gate.Gate
	Timeout     time.Duration
}

type Client struct {
	baseURL   string
	userAgent string
	gate      *gate.Gate
	http      *http.Client
}

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
			// Never follow a redirect: classify reports the 3xx as an error.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (c *Client) BaseURL() string {
	return c.baseURL
}

func (c *Client) Gate() *gate.Gate {
	return c.gate
}

type StatusError struct {
	Path        string
	Status      int
	ContentType string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("Doctolib answered HTTP %d (%s) on %s", e.Status, e.ContentType, e.Path)
}

// Get sends rawQuery unchanged, so the parameters keep their order.
func (c *Client) Get(ctx context.Context, path, rawQuery string) ([]byte, error) {
	return c.do(ctx, http.MethodGet, path, rawQuery, nil)
}

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

// setHeaders gives every request the headers Doctolib's website sends from Chrome.
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

// classify counts an HTML 2xx or 503 as a block because Cloudflare challenges are HTML. Other errors, 403 and
// 429 aside, are not blocks, so an outage leaves no marker for a person to delete.
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

// visibleText is the start of the page's visible text, where a block page shows its support code.
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
