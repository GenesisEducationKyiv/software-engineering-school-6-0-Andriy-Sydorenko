package domain

import (
	"encoding/json"
	"testing"
)

func TestToSubscriptionResponse(t *testing.T) {
	s := &Subscription{
		ID:               99,
		Email:            "a@b.com",
		Repo:             "golang/go",
		Confirmed:        true,
		LastSeenTag:      "v1.22.0",
		UnsubscribeToken: "secret-should-never-leak",
	}
	got := ToSubscriptionResponse(s)

	if got.Email != s.Email || got.Repo != s.Repo ||
		got.Confirmed != s.Confirmed || got.LastSeenTag != s.LastSeenTag {
		t.Fatalf("bad conversion: %+v", got)
	}
}

func TestSubscriptionResponseJSONShape(t *testing.T) {
	resp := SubscriptionResponse{
		Email:       "a@b.com",
		Repo:        "golang/go",
		Confirmed:   true,
		LastSeenTag: "v1",
	}

	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var generic map[string]any
	if err := json.Unmarshal(b, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	wantKeys := []string{"email", "repo", "confirmed", "last_seen_tag"}
	if len(generic) != len(wantKeys) {
		t.Fatalf("want %d keys, got %d: %v", len(wantKeys), len(generic), generic)
	}
	for _, k := range wantKeys {
		if _, ok := generic[k]; !ok {
			t.Fatalf("missing key %q in %v", k, generic)
		}
	}
}

func TestToSubscriptionListResponse(t *testing.T) {
	subs := []Subscription{
		{Email: "a@b.com", Repo: "golang/go"},
		{Email: "a@b.com", Repo: "gin-gonic/gin", Confirmed: true},
	}
	got := ToSubscriptionListResponse(subs)

	if len(got) != 2 {
		t.Fatalf("want 2 responses, got %d", len(got))
	}
	if got[1].Repo != "gin-gonic/gin" || !got[1].Confirmed {
		t.Fatalf("bad element: %+v", got[1])
	}
}
