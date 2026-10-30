package databases

import (
	"errors"
	"strings"
	"testing"
)

func TestRedactStringCoversPrefixedKeysAndColon(t *testing.T) {
	cases := []string{
		"DB_PASSWORD=s3cret",
		"APP_SECRET=s3cret",
		"password: s3cret",
		"PGPASSWORD=s3cret",
		"postgres://app:s3cret@127.0.0.1/app",
		"mysql://app:s3cret@127.0.0.1/app",
	}
	for _, input := range cases {
		got := RedactString(input)
		if strings.Contains(got, "s3cret") {
			t.Fatalf("RedactString(%q) = %q", input, got)
		}
	}
}

func TestRedactErrorHandlesNil(t *testing.T) {
	if got := RedactError(nil); got != "" {
		t.Fatalf("RedactError(nil) = %q", got)
	}
	if got := RedactError(errors.New("APP_SECRET=s3cret")); strings.Contains(got, "s3cret") {
		t.Fatalf("RedactError = %q", got)
	}
}
