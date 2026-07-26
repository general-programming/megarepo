// Package vyoswire is the VyOS HTTPS API wire protocol and nothing else: a
// form-POST of `data` (a JSON op) plus `key`, answered by a
// {success, data, error} envelope.
//
// It holds NO endpoint allowlist, NO write gate and NO authorization logic.
// Read/write separation lives in three independent closed allowlists, each
// deciding for itself whether to call Post; keep them separate:
//
//   - device.vyosRequestAllowed — VyOSReader, `show`/`retrieve` only.
//   - device.vyosWriteRequestAllowed — VyOSWriter, `configure`/
//     `config-file`+save only, reachable only via NewVyOSWriter, which
//     requires Options.AllowWrites.
//   - lifecycle apiEndpoint.write — `/image` delete, gated on
//     APIOptions.AllowWrites.
package vyoswire

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// MaxResponseBytes bounds a response body so a wedged device cannot make
// barf allocate without bound. 64 MiB clears a core router's full
// `/retrieve`; truncating one surfaces as a malformed response.
const MaxResponseBytes = 64 << 20

// Response is the reply envelope every VyOS API endpoint answers with.
type Response struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   any             `json:"error"`
}

// FormBody encodes one request as the API's form payload. The key is a form
// value, never a header or query parameter, and must never be logged.
func FormBody(key string, payload any) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return url.Values{"data": {string(data)}, "key": {key}}.Encode(), nil
}

// Post performs one VyOS API request against an already-built target URL
// and returns the envelope's `data`. NO authorization: the caller's guard
// must already have permitted this endpoint.
func Post(ctx context.Context, client *http.Client, hostname, target, key string, payload any) (json.RawMessage, error) {
	body, err := FormBody(key, payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: vyos api request failed: %w", hostname, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes))
	if err != nil {
		return nil, err
	}
	return Decode(hostname, resp.StatusCode, raw)
}

// Decode turns a raw response body into the envelope's `data`, or the error
// the device reported — never echoing the request, which carries the key.
func Decode(hostname string, statusCode int, raw []byte) (json.RawMessage, error) {
	var decoded Response
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("%s: malformed vyos api response (HTTP %d): %w",
			hostname, statusCode, err)
	}
	if !decoded.Success {
		return nil, fmt.Errorf("%s: vyos api: %s", hostname, decoded.message())
	}
	return decoded.Data, nil
}

// message renders the loosely typed `error` field: usually a string, but
// any JSON value. Empty falls back rather than emitting a dangling colon.
func (r Response) message() string {
	if s, ok := r.Error.(string); ok {
		if s != "" {
			return s
		}
	} else if r.Error != nil {
		if rendered := fmt.Sprint(r.Error); rendered != "" {
			return rendered
		}
	}
	return "unknown API error"
}

// Text interprets an envelope's `data` as op-mode output: a JSON string,
// with anything else surfaced raw rather than dropped.
func Text(data json.RawMessage) string {
	if len(data) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return string(data)
	}
	return text
}
