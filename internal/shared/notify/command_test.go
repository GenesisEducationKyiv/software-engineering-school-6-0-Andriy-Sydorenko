package notify

import "testing"

func TestConfirmationDedupID(t *testing.T) {
	if got := ConfirmationDedupID("tok-123"); got != "confirmation:tok-123" {
		t.Fatalf("got %q", got)
	}
}

func TestReleaseDedupID(t *testing.T) {
	if got := ReleaseDedupID("golang/go", "v1.1", "a@x.com"); got != "release:golang/go:v1.1:a@x.com" {
		t.Fatalf("got %q", got)
	}
}
