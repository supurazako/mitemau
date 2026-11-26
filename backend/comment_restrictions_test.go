package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func restrictionBody(allowed bool, report string) string {
	body, _ := json.Marshal(map[string]any{"postingAllowed": allowed, "reason": "PRIVATE investigation detail", "publicReason": func() string {
		if allowed {
			return ""
		}
		return "Please follow comment rules"
	}(), "reportId": report})
	return string(body)
}
func TestCommentRestrictionsMySQL(t *testing.T) {
	f := moderationDB(t)
	stream := createCommentStream(t, f)
	account := hex.EncodeToString(f.guest)
	path := "/staff/accounts/" + account + "/comment-permission"
	if w := f.call("POST", path, f.userToken, restrictionBody(false, "")); w.Code != 403 {
		t.Fatal("staff only", w.Code)
	}
	if w := f.call("POST", path, "", restrictionBody(false, "")); w.Code != 401 {
		t.Fatal(w.Code)
	}
	postPath := "/streams/" + stream + "/comments"
	body := postCommentBody(hex.EncodeToString(randomModerationID(16)), "before suspension")
	if w := f.call("POST", postPath, f.guestToken, body); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := f.call("POST", path, f.staffToken, restrictionBody(false, "")); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	// A new login session has the same account-wide restriction.
	second := hex.EncodeToString(randomModerationID(32))
	if _, err := f.db.Exec("INSERT INTO account_sessions(token_hash,account_id,expires_at,absolute_expires_at) VALUES (?,?,?,?)", tokenHash(second), f.guest, f.now.Add(time.Hour), f.now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{f.guestToken, second} {
		w := f.call("GET", "/account/comment-permission", token, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Please follow comment rules") || strings.Contains(w.Body.String(), "PRIVATE") {
			t.Fatal("public reason leak/state", w.Code, w.Body.String())
		}
		for _, payload := range []string{body, postCommentBody(hex.EncodeToString(randomModerationID(16)), "blocked")} {
			w = f.call("POST", postPath, token, payload)
			if w.Code != 403 || !strings.Contains(w.Body.String(), "Please follow comment rules") || strings.Contains(w.Body.String(), "PRIVATE") {
				t.Fatal("posting enforcement", w.Code, w.Body.String())
			}
		}
	}
	if w := f.call("GET", "/public/streams/"+stream+"/comments", "", ""); w.Code != 200 {
		t.Fatal("viewing blocked", w.Code)
	}
	if w := f.call("POST", path, f.staffToken, restrictionBody(false, "")); w.Code != 204 {
		t.Fatal(w.Code)
	}
	var count int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM moderation_events WHERE account_id=? AND kind='posting_suspended'", f.guest).Scan(&count); err != nil || count != 1 {
		t.Fatal("idempotency", count, err)
	}
	if w := f.call("POST", path, f.staffToken, restrictionBody(true, "")); w.Code != 204 {
		t.Fatal(w.Body.String())
	}
	w := f.call("GET", "/account/comment-permission", f.guestToken, "")
	if !strings.Contains(w.Body.String(), `"postingAllowed":true`) || !strings.Contains(w.Body.String(), `"reason":null`) {
		t.Fatal(w.Body.String())
	}
	f.now = f.now.Add(commentInterval)
	if w = f.call("POST", postPath, second, postCommentBody(hex.EncodeToString(randomModerationID(16)), "after restoration")); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Staff history contains the private reason; users cannot read that endpoint.
	history := f.path("activity", f.now.Add(-time.Hour), f.now.Add(time.Hour))
	history = strings.ReplaceAll(history, hex.EncodeToString(f.user), account)
	if w = f.call("GET", history, f.staffToken, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "PRIVATE investigation detail") {
		t.Fatal("history", w.Code, w.Body.String())
	}
	if w = f.call("GET", history, f.guestToken, ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestCommentRestrictionReportAndRollbackMySQL(t *testing.T) {
	f := moderationDB(t)
	path := "/staff/accounts/" + hex.EncodeToString(f.guest) + "/comment-permission"
	report := randomModerationID(16)
	raw := hex.EncodeToString(report)
	_, err := f.db.Exec("INSERT INTO user_reports(id,reporter_id,target_id,reason,incident_at,created_at,expires_at) VALUES (?,?,?,?,?,?,?)", report, f.user, f.guest, "report", f.now, f.now, f.now.Add(evidenceRetention))
	if err != nil {
		t.Fatal(err)
	}
	defer f.db.Exec("DELETE FROM user_reports WHERE id=?", report)
	wrong := "/staff/accounts/" + hex.EncodeToString(f.user) + "/comment-permission"
	if w := f.call("POST", wrong, f.staffToken, restrictionBody(false, raw)); w.Code != 400 {
		t.Fatal("wrong subject", w.Code)
	}
	trigger := "permission_audit_" + hex.EncodeToString(randomModerationID(8))
	if _, err = f.db.Exec(fmt.Sprintf("CREATE TRIGGER %s BEFORE INSERT ON comment_permission_actions FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='forced action failure'", trigger)); err != nil {
		t.Fatal(err)
	}
	defer f.db.Exec("DROP TRIGGER IF EXISTS " + trigger)
	if w := f.call("POST", path, f.staffToken, restrictionBody(false, raw)); w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := f.call("GET", "/account/comment-permission", f.guestToken, "")
	if !strings.Contains(w.Body.String(), `"postingAllowed":true`) {
		t.Fatal("permission changed without audit", w.Body.String())
	}
	f.db.Exec("DROP TRIGGER " + trigger)
	if w = f.call("POST", path, f.staffToken, restrictionBody(false, raw)); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	var linked int
	if err = f.db.QueryRow("SELECT COUNT(*) FROM moderation_events WHERE account_id=? AND report_id=? AND kind='posting_suspended'", f.guest, report).Scan(&linked); err != nil || linked != 1 {
		t.Fatal(linked, err)
	}
	f.now = f.now.Add(evidenceRetention)
	if err = f.server.cleanupModeration(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow("SELECT COUNT(*) FROM comment_permission_actions WHERE event_id NOT IN (SELECT id FROM moderation_events)").Scan(&linked); err != nil || linked != 0 {
		t.Fatal("expired action detail", linked, err)
	}
	// Expiry of action history does not lift an active restriction.
	if w = f.call("GET", "/account/comment-permission", f.guestToken, ""); !strings.Contains(w.Body.String(), `"postingAllowed":false`) {
		t.Fatal(w.Body.String())
	}
	if w = f.call("POST", path, f.staffToken, restrictionBody(true, raw)); w.Code != 400 {
		t.Fatal("expired report accepted", w.Code)
	}
}
