package main

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"testing"
	"time"
)

func TestBroadcasterCommentDeletionMySQL(t *testing.T) {
	f := moderationDB(t)
	stream := createCommentStream(t, f)
	other := createCommentStream(t, f)
	requestID := hex.EncodeToString(randomModerationID(16))
	if w := f.call("POST", "/streams/"+stream+"/comments", f.userToken, postCommentBody(requestID, "Retained evidence")); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	before := getCommentSnapshot(t, f, stream)
	id := before.Comments[0].ID
	path := "/streams/" + stream + "/comments/" + id
	permission := "/streams/" + stream + "/comments/permissions"
	for token, want := range map[string]int{"": 401, f.guestToken: 403, f.staffToken: 403} {
		if w := f.call("DELETE", path, token, ""); w.Code != want {
			t.Fatal("non-participant deletion", w.Code, w.Body.String())
		}
	}
	if w := f.call("GET", permission, f.guestToken, ""); w.Body.String() != "{\"canDelete\":false}\n" {
		t.Fatal(w.Body.String())
	}
	if w := f.call("POST", "/streams/"+stream+"/invite", f.userToken, `{"accountId":"`+hex.EncodeToString(f.guest)+`"}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := f.call("DELETE", path, f.guestToken, ""); w.Code != 403 {
		t.Fatal("invitation without participation", w.Code)
	}
	if w := f.call("POST", "/streams/"+stream+"/join", f.guestToken, "{}"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := f.call("GET", permission, f.guestToken, ""); w.Body.String() != "{\"canDelete\":true}\n" {
		t.Fatal(w.Body.String())
	}
	if w := f.call("DELETE", "/streams/"+other+"/comments/"+id, f.userToken, ""); w.Code != 404 {
		t.Fatal("cross-stream", w.Code)
	}
	if w := f.call("DELETE", path, f.guestToken, ""); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	after := getCommentSnapshot(t, f, stream)
	if len(after.Comments) != 0 || after.Version != "2" {
		t.Fatal("public deletion/version", after)
	}
	raw, _ := streamID(id)
	var hidden, expires time.Time
	var body string
	if err := f.db.QueryRow("SELECT hidden_at,expires_at,body FROM moderation_comment_content WHERE comment_id=?", raw).Scan(&hidden, &expires, &body); err != nil || body != "Retained evidence" || !hidden.Equal(f.now) || !expires.Equal(f.now.Add(commentRetention)) {
		t.Fatal("retention", err, hidden, expires, body)
	}
	report := randomModerationID(16)
	f.transaction(t, func(tx *sql.Tx) error {
		return retainCommentEvidence(context.Background(), tx, report, raw, f.now, f.now)
	})
	if err := f.db.QueryRow("SELECT body FROM moderation_evidence WHERE report_id=?", report).Scan(&body); err != nil || body != "Retained evidence" {
		t.Fatal("evidence snapshot", err, body)
	}
	f.now = f.now.Add(time.Hour)
	if w := f.call("DELETE", path, f.userToken, ""); w.Code != 204 {
		t.Fatal("creator retry", w.Code, w.Body.String())
	}
	if getCommentSnapshot(t, f, stream).Version != "2" {
		t.Fatal("retry changed version")
	}
	var count int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM moderation_events WHERE comment_id=? AND kind='comment_deleted' AND actor_id=? AND account_id=?", raw, f.guest, f.user).Scan(&count); err != nil || count != 1 {
		t.Fatal("audit", count, err)
	}
	var retryExpiry time.Time
	if err := f.db.QueryRow("SELECT expires_at FROM moderation_comment_content WHERE comment_id=?", raw).Scan(&retryExpiry); err != nil || !retryExpiry.Equal(expires) {
		t.Fatal("retry extended retention", err)
	}
	if w := f.call("POST", "/streams/"+stream+"/leave", f.guestToken, "{}"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := f.call("DELETE", path, f.guestToken, ""); w.Code != 403 {
		t.Fatal("former participant", w.Code)
	}
	if w := f.call("POST", "/streams/"+stream+"/end", f.userToken, "{}"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := f.call("DELETE", path, f.userToken, ""); w.Code != 409 {
		t.Fatal("ended deletion", w.Code)
	}
}

func TestCommentDeletionAuditRollbackMySQL(t *testing.T) {
	f := moderationDB(t)
	stream := createCommentStream(t, f)
	if w := f.call("POST", "/streams/"+stream+"/comments", f.userToken, postCommentBody(hex.EncodeToString(randomModerationID(16)), "Keep on failure")); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	before := getCommentSnapshot(t, f, stream)
	trigger := "delete_audit_" + hex.EncodeToString(randomModerationID(8))
	if _, err := f.db.Exec(fmt.Sprintf("CREATE TRIGGER %s BEFORE INSERT ON moderation_events FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='forced audit failure'", trigger)); err != nil {
		t.Fatal(err)
	}
	defer f.db.Exec("DROP TRIGGER IF EXISTS " + trigger)
	if w := f.call("DELETE", "/streams/"+stream+"/comments/"+before.Comments[0].ID, f.userToken, ""); w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	after := getCommentSnapshot(t, f, stream)
	if after.Version != before.Version || len(after.Comments) != 1 {
		t.Fatal("deletion committed without audit", after)
	}
}
