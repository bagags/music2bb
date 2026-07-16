package bilibili

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/bagags/music2bb-go/internal/netx"
)

type apiEnvelope struct {
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (c *Client) get(ctx context.Context, client *netx.Client, operation, endpoint string, params url.Values, out any) error {
	return c.getAllowCode(ctx, client, operation, endpoint, params, out, 0)
}

func (c *Client) getAllowCode(ctx context.Context, client *netx.Client, operation, endpoint string, params url.Values, out any, allowedCode int64) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return &APIError{Operation: operation, Err: err}
	}
	query := u.Query()
	for key, values := range params {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return &APIError{Operation: operation, Err: err}
	}
	c.addHeaders(req)
	return c.doAllowCode(client, operation, req, out, allowedCode)
}

func (c *Client) post(ctx context.Context, operation, endpoint string, form url.Values, out any) error {
	body := form.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return &APIError{Operation: operation, Err: err}
	}
	c.addHeaders(req)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// netx deliberately never retries POST requests, even when the server
	// returns a transient status after accepting an ambiguous write.
	return c.do(c.account, operation, req, out)
}

func (c *Client) do(client *netx.Client, operation string, req *http.Request, out any) error {
	return c.doAllowCode(client, operation, req, out, 0)
}

func (c *Client) doAllowCode(client *netx.Client, operation string, req *http.Request, out any, allowedCode int64) error {
	resp, err := client.Do(req)
	if err != nil {
		return &APIError{Operation: operation, Err: err}
	}
	defer resp.Body.Close()
	requestID := firstNonEmpty(resp.Header.Get("x-sec-request-id"), resp.Header.Get("x-bili-trace-id"))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		apiErr := &APIError{Operation: operation, StatusCode: resp.StatusCode, RequestID: requestID, Err: readErr}
		if readErr == nil {
			var envelope apiEnvelope
			if json.Unmarshal(data, &envelope) == nil {
				apiErr.Code = envelope.Code
				apiErr.Message = envelope.Message
			}
		}
		return apiErr
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return &APIError{Operation: operation, RequestID: requestID, Err: err}
	}
	var envelope apiEnvelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil {
		return &APIError{Operation: operation, RequestID: requestID, Err: err}
	}
	if envelope.Code != 0 && envelope.Code != allowedCode {
		return &APIError{Operation: operation, Code: envelope.Code, Message: envelope.Message, RequestID: requestID}
	}
	if out == nil || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil
	}
	decoder = json.NewDecoder(bytes.NewReader(envelope.Data))
	decoder.UseNumber()
	if err := decoder.Decode(out); err != nil {
		return &APIError{Operation: operation, RequestID: requestID, Err: err}
	}
	return nil
}

func (c *Client) addHeaders(req *http.Request) {
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Referer", "https://www.bilibili.com/")
	req.Header.Set("Origin", "https://www.bilibili.com")
}

type flexibleInt64 int64

func (v *flexibleInt64) UnmarshalJSON(data []byte) error {
	if string(data) == "null" || string(data) == `""` || string(data) == `"--"` {
		*v = 0
		return nil
	}
	if len(data) > 0 && data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		parsed, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid integer %q: %w", text, err)
		}
		*v = flexibleInt64(parsed)
		return nil
	}
	parsed, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return err
	}
	*v = flexibleInt64(parsed)
	return nil
}

func formatDuration(seconds int64) string {
	if seconds <= 0 {
		return ""
	}
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
