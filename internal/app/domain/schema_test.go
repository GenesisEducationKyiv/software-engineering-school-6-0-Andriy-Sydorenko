package domain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToSubscriptionResponse(t *testing.T) {
	s := &Subscription{
		ID:               99,
		Email:            "a@b.com",
		Repo:             "golang/go",
		Confirmed:        true,
		UnsubscribeToken: "secret-should-never-leak",
	}
	got := ToSubscriptionResponse(s)

	assert.Equal(t, s.Email, got.Email)
	assert.Equal(t, s.Repo, got.Repo)
	assert.Equal(t, s.Confirmed, got.Confirmed)
}

func TestSubscriptionResponseJSONShape(t *testing.T) {
	resp := SubscriptionResponse{
		Email:     "a@b.com",
		Repo:      "golang/go",
		Confirmed: true,
	}

	b, err := json.Marshal(resp)
	require.NoError(t, err)

	var generic map[string]any
	require.NoError(t, json.Unmarshal(b, &generic))

	wantKeys := []string{"email", "repo", "confirmed"}
	assert.Len(t, generic, len(wantKeys), "no unexpected fields in public JSON")
	for _, k := range wantKeys {
		assert.Contains(t, generic, k)
	}
}

func TestToSubscriptionListResponse(t *testing.T) {
	subs := []Subscription{
		{Email: "a@b.com", Repo: "golang/go"},
		{Email: "a@b.com", Repo: "gin-gonic/gin", Confirmed: true},
	}
	got := ToSubscriptionListResponse(subs)

	require.Len(t, got, 2)
	assert.Equal(t, "gin-gonic/gin", got[1].Repo)
	assert.True(t, got[1].Confirmed)
}
