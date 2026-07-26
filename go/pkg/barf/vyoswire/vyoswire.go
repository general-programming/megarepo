// Package vyoswire is the VyOS HTTPS API wire protocol, and nothing else.
//
// The whole protocol is a form-POST of two fields — `data` (a JSON op)
// plus `key` — answered by a {success, data, error} JSON envelope. There
// is no Go client library for it, so barf hand-rolls it. It was hand-
// rolled three times: once in device.VyOSReader, once in
// device.VyOSWriter and once in lifecycle.APIClient, and the three copies
// had already drifted — lifecycle capped the response body at 16 MiB
// where device capped it at 64 MiB, so a large `/retrieve` truncated on
// the lifecycle path only and surfaced as "malformed vyos api response".
// This package is the single copy.
//
// # What this package deliberately does NOT do
//
// It holds NO endpoint allowlist, NO write gate and NO authorization
// logic of any kind. Post takes a fully-formed URL and posts to it.
//
// That is the point. barf's read/write separation is enforced by three
// independent, closed allowlists that live with the code that owns them:
//
//   - device.vyosRequestAllowed — VyOSReader, `show`/`retrieve` only.
//   - device.vyosWriteRequestAllowed — VyOSWriter, `configure`/
//     `config-file`+save only, and only reachable via NewVyOSWriter,
//     which requires Options.AllowWrites.
//   - lifecycle's apiEndpoint.write flag — `/image` delete, gated on
//     APIOptions.AllowWrites.
//
// Each of those guards runs in its own package, before it builds a URL,
// and decides for itself whether to call Post at all. Sharing the codec
// does not join them: Options.AllowWrites still has no effect whatsoever
// on the read guard, and VyOSReader still cannot name a write endpoint.
// Moving a guard in here would have merged three separate decisions into
// one switch, which is exactly the property worth keeping, so the guards
// stayed put and only the plumbing moved.
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

// MaxResponseBytes bounds a response body.
//
// 64 MiB, the larger of the two limits the duplicated copies had grown.
// A full `/retrieve` of a core router's config is the big one; the old
// 16 MiB cap in lifecycle truncated it mid-JSON, and the truncation
// surfaced as a confusing "malformed vyos api response" rather than as a
// size error. The limit exists only so a wedged or hostile device cannot
// make barf allocate without bound.
const MaxResponseBytes = 64 << 20

// Response is the VyOS API's reply envelope: every endpoint answers with
// this shape regardless of what was asked.
type Response struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   any             `json:"error"`
}

// FormBody encodes one request as the API's form payload. The key is a
// form value, never a header or a query parameter, and it must never be
// logged — callers that log requests must log the payload only.
func FormBody(key string, payload any) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return url.Values{"data": {string(data)}, "key": {key}}.Encode(), nil
}

// Post performs one VyOS API request against an already-built target URL
// and returns the envelope's `data` on success.
//
// It performs NO authorization: the caller's own guard must already have
// decided that this endpoint is permitted. hostname is used only to
// prefix error messages, so a fleet-wide run says which device failed.
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

// Decode turns a raw response body into the envelope's `data`, or into
// the error the device reported.
//
// The error text never includes the request, because the request carries
// the API key. Only the device's own message comes back.
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

// message renders the `error` field, which the API types loosely: it is
// usually a string but can be any JSON value.
//
// All three copies this replaced fell through to fmt.Sprint for an empty
// string, so a device answering {"success":false,"error":""} produced the
// message "vyos api: " with nothing after it. The empty case now lands on
// the same fallback as a missing one: an operator reading a failed deploy
// is better served by "unknown API error" than by a dangling colon.
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

// Text interprets an envelope's `data` as op-mode output. Op-mode
// endpoints answer with a JSON string; anything else is surfaced raw
// rather than dropped.
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
