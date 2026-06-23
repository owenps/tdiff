package github

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type PRStatus string

const (
	PRStatusReady   PRStatus = "ready"
	PRStatusDraft   PRStatus = "draft"
	PRStatusBehind  PRStatus = "behind"
	PRStatusBlocked PRStatus = "blocked"
	PRStatusMerged  PRStatus = "merged"
	PRStatusClosed  PRStatus = "closed"
)

func derivePRStatus(state string, mergedAt *time.Time, mergeable, mergeStateStatus, reviewDecision string, isDraft bool, statusCheckRollup json.RawMessage) PRStatus {
	state = strings.ToUpper(state)
	mergeable = strings.ToUpper(mergeable)
	mergeStateStatus = strings.ToUpper(mergeStateStatus)
	reviewDecision = strings.ToUpper(reviewDecision)

	if state == "MERGED" || mergedAt != nil {
		return PRStatusMerged
	}
	if state == "CLOSED" {
		return PRStatusClosed
	}
	checksFailing, checksPending := checkRollupState(statusCheckRollup)
	if mergeable == "CONFLICTING" || mergeable == "DIRTY" || mergeable == "BLOCKED" || mergeStateStatus == "DIRTY" || mergeStateStatus == "BLOCKED" || checksFailing {
		return PRStatusBlocked
	}
	if mergeStateStatus == "BEHIND" {
		return PRStatusBehind
	}
	if isDraft {
		return PRStatusDraft
	}
	if mergeable == "MERGEABLE" && !checksPending {
		return PRStatusReady
	}
	return ""
}

func checkRollupState(raw json.RawMessage) (failing, pending bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return false, false
	}
	var nodes []map[string]any
	if err := json.Unmarshal(raw, &nodes); err != nil {
		return false, false
	}
	for _, n := range nodes {
		if isFailingCheckValue(n["conclusion"]) || isFailingCheckValue(n["state"]) || isFailingCheckValue(n["status"]) {
			failing = true
		}
		if isPendingCheckValue(n["state"]) || isPendingCheckValue(n["status"]) {
			pending = true
		}
		if n["conclusion"] == nil && !isSuccessfulCheckValue(n["state"]) && !isSuccessfulCheckValue(n["status"]) {
			pending = true
		}
	}
	return failing, pending
}

func checkValue(v any) string {
	if v == nil {
		return ""
	}
	return strings.ToUpper(strings.TrimSpace(fmt.Sprint(v)))
}

func isFailingCheckValue(v any) bool {
	switch checkValue(v) {
	case "FAILURE", "FAILED", "ERROR", "CANCELLED", "CANCELED", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE":
		return true
	default:
		return false
	}
}

func isPendingCheckValue(v any) bool {
	switch checkValue(v) {
	case "PENDING", "QUEUED", "IN_PROGRESS", "WAITING", "REQUESTED", "EXPECTED", "IN_PROGRESSING":
		return true
	default:
		return false
	}
}

func isSuccessfulCheckValue(v any) bool {
	switch checkValue(v) {
	case "SUCCESS", "SUCCESSFUL", "COMPLETED", "SKIPPED", "NEUTRAL":
		return true
	default:
		return false
	}
}
