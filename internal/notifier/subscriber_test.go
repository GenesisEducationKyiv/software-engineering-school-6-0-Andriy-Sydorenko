package notifier

import (
	"errors"
	"fmt"
	"testing"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		numDelivered int
		maxDeliver   int
		want         action
	}{
		{"success", nil, 1, 5, actionAck},
		{"transient first try", errors.New("smtp down"), 1, 5, actionNak},
		{"transient exhausted", errors.New("smtp down"), 5, 5, actionTerm},
		{"permanent", fmt.Errorf("%w: bad json", ErrPermanent), 1, 5, actionTerm},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classify(tc.err, tc.numDelivered, tc.maxDeliver); got != tc.want {
				t.Fatalf("classify=%v want=%v", got, tc.want)
			}
		})
	}
}
