package log

import "net/url"

// RedactURL replaces user:password in a URL with user:***.
// Returns the original string if it cannot be parsed as a URL.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	return u.Redacted()
}