package dbengine

import "testing"

func TestValidateIdentifierRejectsUnsafeNames(t *testing.T) {
	for _, name := range []string{"", "app;drop", "app`db", "app\n", "../app", "app db"} {
		if err := ValidateIdentifier(name); err == nil {
			t.Fatalf("ValidateIdentifier(%q) = nil", name)
		}
	}
	if err := ValidateIdentifier("app_1"); err != nil {
		t.Fatalf("valid name rejected: %v", err)
	}
}

func TestQuoteIdentifierUsesEngineQuotes(t *testing.T) {
	got, err := QuoteIdentifier("mysql", "app")
	if err != nil || got != "`app`" {
		t.Fatalf("mysql quote = %q err=%v", got, err)
	}
	got, err = QuoteIdentifier("postgres", "app")
	if err != nil || got != `"app"` {
		t.Fatalf("postgres quote = %q err=%v", got, err)
	}
}
