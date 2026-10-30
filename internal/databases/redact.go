package databases

import (
	"regexp"
	"strings"
)

// Redaction patterns cover the credential shapes engine tools and connection
// strings commonly put in error messages. The manager redacts at its boundary
// so no secret reaches audit rows, job rows, events, or API responses.
var redactionPatterns = []*regexp.Regexp{
	// PASSWORD=..., DB_PASSWORD=..., APP_SECRET: ..., PGPASSWORD=...
	regexp.MustCompile(`(?i)\b[A-Z0-9_]*(?:PASSWORD|PASSWD|PWD|SECRET|TOKEN)[A-Z0-9_]*\s*[=:]\s*[^\s;&"',}]+`),
	regexp.MustCompile(`(?i)\b(mysql|mariadb|postgres|postgresql|redis)://[^@\s/]+@`),
	regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^/\s:@]+:[^@\s/]+@`),
}

// RedactString removes credential values from one message.
func RedactString(value string) string {
	redacted := value
	for _, pattern := range redactionPatterns {
		redacted = pattern.ReplaceAllStringFunc(redacted, func(match string) string {
			lower := strings.ToLower(match)
			if strings.Contains(lower, "://") {
				index := strings.Index(match, "://")
				return match[:index+3] + "***@"
			}
			if index := strings.IndexAny(match, "=:"); index >= 0 {
				return match[:index+1] + "***"
			}
			return "***"
		})
	}
	return redacted
}

// RedactError returns a redacted error message, or an empty string for nil.
func RedactError(err error) string {
	if err == nil {
		return ""
	}
	return RedactString(err.Error())
}
