package metrics

import "testing"

func TestRecorderSnapshotAndRecentErrors(t *testing.T) {
	recorder := NewRecorder(2)
	recorder.RecordRoutingDecision(RoutingDecision{TaskType: "code", MatchedRule: "code-rule", PreferTiers: []string{"advanced"}, SelectedAccount: "acct_1"})
	recorder.RecordRequest(RequestResult{AccountID: "acct_1", StatusCode: 200, Success: true})
	recorder.RecordCooldown("acct_1")
	recorder.RecordQuotaSwitch("acct_1")
	recorder.RecordRequest(RequestResult{AccountID: "acct_1", StatusCode: 429, Success: false, Error: "rate limited"})
	recorder.RecordRequest(RequestResult{AccountID: "acct_2", StatusCode: 502, Success: false, Error: "Bearer sk-secret leaked"})
	recorder.RecordRequest(RequestResult{AccountID: "acct_3", StatusCode: 400, Success: false, Error: "bad request"})

	snapshot := recorder.Snapshot()
	if snapshot.TotalRequests != 4 || snapshot.SuccessRequests != 1 || snapshot.ErrorRequests != 3 {
		t.Fatalf("unexpected counters: %#v", snapshot)
	}
	if snapshot.PerAccountHits["acct_1"] != 1 || snapshot.PerAccountErrors["acct_1"] != 1 {
		t.Fatalf("unexpected account counters: %#v %#v", snapshot.PerAccountHits, snapshot.PerAccountErrors)
	}
	if snapshot.CooldownCounts["acct_1"] != 1 || snapshot.QuotaSwitchCounts["acct_1"] != 1 {
		t.Fatalf("unexpected cooldown/quota counters: %#v %#v", snapshot.CooldownCounts, snapshot.QuotaSwitchCounts)
	}
	if len(snapshot.RoutingDecisions) != 1 || snapshot.RoutingDecisions[0].SelectedAccount != "acct_1" {
		t.Fatalf("routing decisions = %#v", snapshot.RoutingDecisions)
	}
	if len(snapshot.RecentErrors) != 2 {
		t.Fatalf("recent error ring length = %d", len(snapshot.RecentErrors))
	}
	for _, item := range snapshot.RecentErrors {
		if item.Message == "Bearer sk-secret leaked" {
			t.Fatalf("secret-bearing message was not redacted: %#v", snapshot.RecentErrors)
		}
	}
}
