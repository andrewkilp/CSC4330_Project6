package webhooks

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"net/url"
	"sort"
	"strings"
)

// expectedSignature implements the Auth Token form algorithm documented by
// Twilio. All decoded form fields participate, including unknown fields. Repeated
// values are sorted and deduplicated like the official Node/Python validators.
func expectedSignature(token, canonicalURL string, form url.Values) []byte {
	keys := make([]string, 0, len(form))
	for key := range form {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	mac := hmac.New(sha1.New, []byte(token))
	_, _ = mac.Write([]byte(canonicalURL))
	for _, key := range keys {
		values := append([]string(nil), form[key]...)
		sort.Strings(values)
		for i, value := range values {
			if i > 0 && values[i-1] == value {
				continue
			}
			_, _ = mac.Write([]byte(key))
			_, _ = mac.Write([]byte(value))
		}
	}
	return mac.Sum(nil)
}

func validSignature(token, canonicalURL, canonicalNoPort, signature string, form url.Values) bool {
	if len(signature) != base64.StdEncoding.EncodedLen(sha1.Size) {
		return false
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(signature)
	if err != nil || len(decoded) != sha1.Size {
		return false
	}
	withPort := hmac.Equal(decoded, expectedSignature(token, canonicalURL, form))
	withoutPort := hmac.Equal(decoded, expectedSignature(token, canonicalNoPort, form))
	return withPort || withoutPort
}

func noPortOrigin(u *url.URL) string {
	host := u.Hostname()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "https://" + host
}
