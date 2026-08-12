package cache

import "strings"

// This file adds safety guards so the cache never returns a stale or unsafe
// answer just because two prompts look similar. A prompt is only cacheable when
// it is not time-sensitive, personalized, or otherwise unsafe to reuse.

var timeSensitiveMarkers = []string{
	"today", "right now", "current time", "currently", "latest", "as of now",
	"this week", "this month", "this year", "breaking news", "stock price",
	"weather", "score", "who won", "recent", "up to date", "live ",
}

var personalizedMarkers = []string{
	"my name", "my account", "my order", "my balance", "my password",
	"remember that i", "i am ", "i'm ", "for me specifically", "my email",
	"my phone", "my address",
}

var sensitiveMarkers = []string{
	"password", "api key", "secret", "ssn", "social security", "credit card",
	"private key", "credentials", "token",
}

// Cacheable reports whether a prompt may be cached AND served from cache. This
// is deliberately conservative: when in doubt, do not cache. Callers pass the
// full rendered prompt (system + user).
func Cacheable(prompt string) bool {
	p := strings.ToLower(prompt)
	for _, m := range timeSensitiveMarkers {
		if strings.Contains(p, m) {
			return false
		}
	}
	for _, m := range personalizedMarkers {
		if strings.Contains(p, m) {
			return false
		}
	}
	for _, m := range sensitiveMarkers {
		if strings.Contains(p, m) {
			return false
		}
	}
	return true
}
