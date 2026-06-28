package domain

type SubscriptionResponse struct {
	Email     string `json:"email"`
	Repo      string `json:"repo"`
	Confirmed bool   `json:"confirmed"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

func ToSubscriptionResponse(s *Subscription) SubscriptionResponse {
	return SubscriptionResponse{
		Email:     s.Email,
		Repo:      s.Repo,
		Confirmed: s.Confirmed,
	}
}

func ToSubscriptionListResponse(subs []Subscription) []SubscriptionResponse {
	result := make([]SubscriptionResponse, len(subs))
	for i := range subs {
		result[i] = ToSubscriptionResponse(&subs[i])
	}
	return result
}
