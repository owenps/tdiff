package github

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDerivePRStatus(t *testing.T) {
	mergedAt := time.Unix(1, 0)
	cases := []struct {
		name           string
		state          string
		mergedAt       *time.Time
		mergeable      string
		reviewDecision string
		isDraft        bool
		checks         string
		want           PRStatus
	}{
		{name: "merged", state: "MERGED", want: PRStatusMerged},
		{name: "merged at", state: "CLOSED", mergedAt: &mergedAt, want: PRStatusMerged},
		{name: "closed", state: "CLOSED", want: PRStatusClosed},
		{name: "conflict", state: "OPEN", mergeable: "CONFLICTING", want: PRStatusBlocked},
		{name: "failing checks", state: "OPEN", mergeable: "MERGEABLE", checks: `[{"conclusion":"FAILURE","status":"COMPLETED"}]`, want: PRStatusBlocked},
		{name: "changes requested", state: "OPEN", mergeable: "MERGEABLE", reviewDecision: "CHANGES_REQUESTED", want: PRStatusChanges},
		{name: "draft", state: "OPEN", mergeable: "MERGEABLE", isDraft: true, want: PRStatusDraft},
		{name: "ready approved", state: "OPEN", mergeable: "MERGEABLE", reviewDecision: "APPROVED", checks: `[{"conclusion":"SUCCESS","status":"COMPLETED"}]`, want: PRStatusReady},
		{name: "ready no review", state: "OPEN", mergeable: "MERGEABLE", checks: `[]`, want: PRStatusReady},
		{name: "pending hidden", state: "OPEN", mergeable: "MERGEABLE", reviewDecision: "APPROVED", checks: `[{"status":"IN_PROGRESS"}]`, want: ""},
		{name: "review pending hidden", state: "OPEN", mergeable: "MERGEABLE", reviewDecision: "REVIEW_REQUIRED", checks: `[]`, want: ""},
		{name: "unknown hidden", state: "OPEN", mergeable: "UNKNOWN", checks: `[]`, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := derivePRStatus(tc.state, tc.mergedAt, tc.mergeable, tc.reviewDecision, tc.isDraft, json.RawMessage(tc.checks))
			if got != tc.want {
				t.Fatalf("status = %q, want %q", got, tc.want)
			}
		})
	}
}
