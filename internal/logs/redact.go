package logs

import "regexp"

// Secret redaction patterns. Assignment keys match the environment variables
// the spec calls out (*KEY*, *TOKEN*, *SECRET*, *PASSWORD*) in both KEY=value
// and KEY: value forms; Authorization headers are removed whole because their
// value is not limited to one token.
var (
	assignmentSecretPattern = regexp.MustCompile(
		`(?i)\b([A-Za-z0-9_.-]*(?:key|token|secret|password)[A-Za-z0-9_.-]*)\b(\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s,;]+)`)
	authorizationPattern    = regexp.MustCompile(`(?i)\b(authorization)\b(\s*:\s*)([^\r\n]+)`)
	jsonQuotedSecretPattern = regexp.MustCompile(
		`(?i)("(?:[^"\\]|\\.)*(?:key|token|secret|password|authorization)(?:[^"\\]|\\.)*")(\s*:\s*)("(?:[^"\\]|\\.)*")`)
	jsonUnquotedSecretPattern = regexp.MustCompile(
		`(?i)("(?:[^"\\]|\\.)*(?:key|token|secret|password|authorization)(?:[^"\\]|\\.)*")(\s*:\s*)([^\s,;}]+)`)
	uriUserinfoPattern = regexp.MustCompile(
		`(?i)\b([a-z][a-z0-9+.-]*://)([^/\s:@]+):([^@\s/]+)@`)
	cookiePattern = regexp.MustCompile(`(?i)\b((?:set-)?cookie)\b(\s*:\s*)([^\r\n]+)`)
)

// Redact replaces secret values in text with "***". It recognises environment
// assignments whose name contains key, token, secret, or password, and any
// Authorization header value. The key or header name is preserved so the log
// still shows what was omitted. Redact never fails and is safe on arbitrary
// process output.
func Redact(text string) string {
	if text == "" {
		return text
	}
	text = jsonQuotedSecretPattern.ReplaceAllString(text, `${1}${2}"***"`)
	text = jsonUnquotedSecretPattern.ReplaceAllString(text, "${1}${2}***")
	text = uriUserinfoPattern.ReplaceAllString(text, "${1}${2}:***@")
	text = cookiePattern.ReplaceAllString(text, "${1}${2}***")
	text = authorizationPattern.ReplaceAllString(text, "${1}${2}***")
	return assignmentSecretPattern.ReplaceAllString(text, "${1}${2}***")
}
