package main

import (
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

func TestReportsMySQL(t *testing.T) {
	f := moderationDB(t)
	stream := createCommentStream(t, f)
	if w := f.call("POST", "/streams/"+stream+"/comments", f.userToken, postCommentBody(hex.EncodeToString(randomModerationID(16)), "evidence body")); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	comment := getCommentSnapshot(t, f, stream).Comments[0].ID
	payload, _ := json.Marshal(map[string]string{"targetId": hex.EncodeToString(f.user), "reason": "test incident", "incidentAt": f.now.Format(time.RFC3339Nano), "streamId": stream, "commentId": comment})
	if w := f.call("POST", "/reports", "", string(payload)); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := f.call("POST", "/reports", f.userToken, string(payload)); w.Code != 400 {
		t.Fatal("self report", w.Code)
	}
	w := f.call("POST", "/reports", f.guestToken, string(payload))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	raw, _ := streamID(result.ID)
	defer f.db.Exec("DELETE FROM user_reports WHERE id=?", raw)
	if w = f.call("POST", "/reports", f.guestToken, string(payload)); w.Code != 429 {
		t.Fatal("cooldown", w.Code)
	}
	if w = f.call("GET", "/staff/reports", f.guestToken, ""); w.Code != 403 {
		t.Fatal("staff check", w.Code)
	}
	if w = f.call("GET", "/staff/reports/"+result.ID+"/evidence", f.guestToken, ""); w.Code != 403 {
		t.Fatal("evidence staff check", w.Code)
	}
	if w = f.call("POST", "/staff/reports/"+result.ID+"/delete-comment", f.guestToken, "{}"); w.Code != 403 {
		t.Fatal("deletion staff check", w.Code)
	}
	if w = f.call("POST", "/staff/reports/"+result.ID+"/delete-comment", f.staffToken, "{}"); w.Code != 204 {
		t.Fatal("staff delete", w.Code, w.Body.String())
	}
	if snap := getCommentSnapshot(t, f, stream); len(snap.Comments) != 0 || snap.Version != "2" {
		t.Fatal("deleted snapshot", snap)
	}
	var body string
	if err := f.db.QueryRow("SELECT body FROM moderation_evidence WHERE report_id=?", raw).Scan(&body); err != nil || body != "evidence body" {
		t.Fatal("evidence", body, err)
	}
	f.now = f.now.Add(24 * time.Hour)
	if w = f.call("POST", "/staff/reports/"+result.ID+"/resolve", f.staffToken, `{"outcome":"Reviewed"}`); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = f.call("POST", "/staff/reports/"+result.ID+"/resolve", f.staffToken, `{"outcome":"Again"}`); w.Code != 409 {
		t.Fatal(w.Code)
	}
	f.now = f.now.Add(evidenceRetention - 24*time.Hour)
	if w = f.call("GET", "/staff/reports", f.staffToken, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w = f.call("GET", "/staff/reports/"+result.ID+"/evidence", f.staffToken, ""); w.Code != 404 {
		t.Fatal("expired evidence", w.Code)
	}
	if err := f.server.cleanupModeration(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM report_outcomes WHERE report_id=?", raw).Scan(&count); err != nil || count != 1 {
		t.Fatal("outcome expires at action deadline", count, err)
	}
	defer f.db.Exec("DELETE FROM report_outcomes WHERE report_id=?", raw)
	if err := f.db.QueryRow("SELECT COUNT(*) FROM user_reports WHERE id=?", raw).Scan(&count); err != nil || count != 0 {
		t.Fatal("retention", count, err)
	}
}
