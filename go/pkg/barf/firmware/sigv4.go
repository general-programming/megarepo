package firmware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// AWS SigV4 signing, enough for this package's two S3 calls (HeadObject and
// PutObject). Hand-rolled rather than adding aws-sdk-go-v2 to the repo's
// single go.mod: neither response needs XML parsing. Switch to the SDK if the
// mirror grows listing, multipart, or presigning.

// unsignedPayload streams a large image body without hashing it first; R2 and
// S3 both accept it over HTTPS.
const unsignedPayload = "UNSIGNED-PAYLOAD"

// Credentials are the mirror bucket's S3 API credentials, from Vault in
// production (secret/r2-firmware).
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string // optional; R2 tokens do not use one
}

// signV4 signs req in place with the SigV4 Authorization header.
// payloadHash is the hex sha256 of the body, or unsignedPayload.
func signV4(req *http.Request, creds Credentials, region, service, payloadHash string, now time.Time) error {
	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" {
		return fmt.Errorf("firmware: mirror credentials are incomplete")
	}
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	if req.Host == "" {
		req.Host = req.URL.Host
	}
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if creds.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", creds.SessionToken)
	}

	signedHeaders, canonicalHeaders := canonicalizeHeaders(req)

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL.EscapedPath()),
		req.URL.RawQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := strings.Join([]string{dateStamp, region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hexSHA256([]byte(canonicalRequest)),
	}, "\n")

	key := hmacSHA256([]byte("AWS4"+creds.SecretAccessKey), dateStamp)
	key = hmacSHA256(key, region)
	key = hmacSHA256(key, service)
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, stringToSign))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		creds.AccessKeyID, scope, signedHeaders, signature,
	))
	return nil
}

// canonicalizeHeaders returns the signed-header list and canonical block.
// Only host and x-amz-* are signed: S3's minimum, and it keeps
// transport-added headers out of the signature.
func canonicalizeHeaders(req *http.Request) (signed, canonical string) {
	names := []string{"host"}
	values := map[string]string{"host": req.Host}
	for name, vs := range req.Header {
		lower := strings.ToLower(name)
		if !strings.HasPrefix(lower, "x-amz-") || len(vs) == 0 {
			continue
		}
		names = append(names, lower)
		values[lower] = strings.TrimSpace(vs[0])
	}
	sortStrings(names)

	var b strings.Builder
	for _, n := range names {
		b.WriteString(n)
		b.WriteString(":")
		b.WriteString(values[n])
		b.WriteString("\n")
	}
	return strings.Join(names, ";"), b.String()
}

// canonicalURI re-escapes a path as SigV4 wants: every segment
// RFC3986-escaped, slashes preserved.
func canonicalURI(escapedPath string) string {
	if escapedPath == "" {
		return "/"
	}
	segments := strings.Split(escapedPath, "/")
	for i, s := range segments {
		segments[i] = escapeSegment(unescapeSegment(s))
	}
	return strings.Join(segments, "/")
}

func escapeSegment(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// unescapeSegment reverses percent-encoding without touching '+', which
// QueryUnescape would eat.
func unescapeSegment(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if v, err := hex.DecodeString(s[i+1 : i+3]); err == nil {
				b.Write(v)
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func hexSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// sortStrings is a tiny insertion sort; the header list is a handful of entries.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
