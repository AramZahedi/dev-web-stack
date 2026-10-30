package agentdocker

import "regexp"

var (
	sqlIdentifiedByPattern = regexp.MustCompile(`(?i)(IDENTIFIED\s+(?:WITH\s+\S+\s+)?BY)\s+'(?:[^']|'')*'`)
	sqlPasswordPattern     = regexp.MustCompile(`(?i)\b(PASSWORD)\s+'(?:[^']|'')*'`)
)

// redactSQLSecrets removes password-bearing SQL fragments from engine output
// before it reaches a caller, a job message, or an HTTP error response. psql
// and mysql can echo the failing statement, including IDENTIFIED BY/PASSWORD.
func redactSQLSecrets(value string) string {
	value = sqlIdentifiedByPattern.ReplaceAllString(value, "$1 '[redacted]'")
	value = sqlPasswordPattern.ReplaceAllString(value, "$1 '[redacted]'")
	return value
}

// redactedError keeps the original error available for errors.Is/As while
// returning only the redacted message.
type redactedError struct {
	message string
	cause   error
}

func (e *redactedError) Error() string { return e.message }
func (e *redactedError) Unwrap() error { return e.cause }

func redactSQLError(err error) error {
	if err == nil {
		return nil
	}
	redacted := redactSQLSecrets(err.Error())
	if redacted == err.Error() {
		return err
	}
	return &redactedError{message: redacted, cause: err}
}
