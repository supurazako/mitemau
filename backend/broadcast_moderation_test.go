package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func broadcastBody(allowed bool, report string) string {
	body, _ := json.Marshal(map[string]any{"broadcastingAllowed": allowed, "reason": "PRIVATE staff assessment", "publicReason": func() string {
		if allowed {
			return ""
		}
		return "Please follow broadcasting rules"
	}(), "reportId": report})
	return string(body)
}
func sharedModerationStream(t *testing.T, f *moderationFixture) string {
	t.Helper()
	id := createCommentStream(t, f)
	if w := f.call("POST", "/streams/"+id+"/invite", f.userToken, `{"accountId":"`+hex.EncodeToString(f.guest)+`"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := f.call("POST", "/streams/"+id+"/join", f.guestToken, "{}"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	return id
}
func prepareModerationPublisher(t *testing.T, f *moderationFixture, id, login string) (string, string) {
	t.Helper()
	session := hex.EncodeToString(randomModerationID(16))
	w := f.call("POST", "/streams/"+id+"/publishing", login, `{"sessionId":"`+session+`","prepare":true}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatal("invalid grant")
	}
	return session, result.Token
}
func connectModerationPublisher(t *testing.T, f *moderationFixture, id, login string) (string, string) {
	t.Helper()
	session, token := prepareModerationPublisher(t, f, id, login)
	if w := commentService(f, "/relay/authorize", `{"token":"`+token+`"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := f.call("POST", "/streams/"+id+"/publishing", login, `{"sessionId":"`+session+`"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	return session, token
}
func TestBroadcastSuspensionMySQL(t *testing.T) {
	for _, role := range []string{"creator", "guest"} {
		t.Run(role, func(t *testing.T) {
			f := moderationDB(t)
			id := sharedModerationStream(t, f)
			ownerSession, ownerToken := connectModerationPublisher(t, f, id, f.userToken)
			guestSession, guestToken := connectModerationPublisher(t, f, id, f.guestToken)
			account, login, oldSession, oldToken, otherToken := f.user, f.userToken, ownerSession, ownerToken, guestToken
			if role == "guest" {
				account, login, oldSession, oldToken, otherToken = f.guest, f.guestToken, guestSession, guestToken, ownerToken
			}
			pending, pendingToken := prepareModerationPublisher(t, f, id, login)
			path := "/staff/accounts/" + hex.EncodeToString(account) + "/broadcast-permission"
			if w := f.call("POST", path, f.userToken, broadcastBody(false, "")); w.Code != 403 {
				t.Fatal("staff only", w.Code)
			}
			if w := f.call("POST", path, f.staffToken, broadcastBody(false, "")); w.Code != 204 {
				t.Fatal(w.Code, w.Body.String())
			}
			if w := f.call("GET", "/account/broadcaster", login, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"suspended"`) || !strings.Contains(w.Body.String(), "Please follow broadcasting rules") || strings.Contains(w.Body.String(), "PRIVATE") {
				t.Fatal("reason/status", w.Body.String())
			}
			for _, token := range []string{oldToken, pendingToken} {
				if w := commentService(f, "/relay/authorize", `{"token":"`+token+`"}`); w.Code != 403 {
					t.Fatal("suspended Relay grant", w.Code)
				}
			}
			if w := commentService(f, "/relay/authorize", `{"token":"`+otherToken+`"}`); w.Code != 200 {
				t.Fatal("other publisher stopped", w.Code, w.Body.String())
			}
			for _, session := range []string{pending, hex.EncodeToString(randomModerationID(16))} {
				if w := f.call("POST", "/streams/"+id+"/publishing", login, `{"sessionId":"`+session+`","prepare":true}`); w.Code != 403 {
					t.Fatal("suspended prepare", w.Code)
				}
			}
			if w := f.call("POST", "/streams", login, `{"id":"`+hex.EncodeToString(randomModerationID(16))+`","title":"blocked"}`); w.Code != 403 {
				t.Fatal("create bypass", w.Code)
			}
			if w := f.call("POST", "/streams/"+id+"/join", login, "{}"); w.Code != 403 {
				t.Fatal("join bypass", w.Code)
			}
			inviteTarget := f.guest
			if role == "guest" {
				inviteTarget = f.guest
			}
			if w := f.call("POST", "/streams/"+id+"/invite", f.userToken, `{"accountId":"`+hex.EncodeToString(inviteTarget)+`"}`); w.Code != 403 {
				t.Fatal("invite bypass", w.Code)
			}
			raw, _ := streamID(id)
			var active int
			if err := f.db.QueryRow("SELECT COUNT(*) FROM stream_participants WHERE stream_id=? AND left_at IS NULL", raw).Scan(&active); err != nil || active != 2 {
				t.Fatal("suspension changed participation", active, err)
			}
			if w := f.call("GET", "/public/streams/"+id, "", ""); w.Code != 200 || strings.Contains(w.Body.String(), oldSession) {
				t.Fatal("public stream/namespace", w.Code, w.Body.String())
			}
			if w := f.call("POST", path, f.staffToken, broadcastBody(false, "")); w.Code != 204 {
				t.Fatal(w.Code)
			}
			if w := f.call("POST", path, f.staffToken, broadcastBody(true, "")); w.Code != 204 {
				t.Fatal(w.Code, w.Body.String())
			}
			for _, token := range []string{oldToken, pendingToken} {
				if w := commentService(f, "/relay/authorize", `{"token":"`+token+`"}`); w.Code != 403 {
					t.Fatal("restoration revived credential", w.Code)
				}
			}
			if w := f.call("POST", "/streams/"+id+"/publishing", login, `{"sessionId":"`+oldSession+`","prepare":true}`); w.Code != 409 {
				t.Fatal("restoration revived session", w.Code)
			}
			connectModerationPublisher(t, f, id, login)
			if w := f.call("POST", path, f.staffToken, broadcastBody(false, "")); w.Code != 204 {
				t.Fatal(w.Code)
			}
			if role == "creator" {
				if w := f.call("POST", "/streams/"+id+"/end", login, "{}"); w.Code != 200 {
					t.Fatal("suspended creator cannot end", w.Code, w.Body.String())
				}
			} else {
				if w := f.call("POST", "/streams/"+id+"/leave", login, "{}"); w.Code != 200 {
					t.Fatal("suspended guest cannot leave", w.Code)
				}
			}
		})
	}
}
func TestForcedStreamEndMySQL(t *testing.T) {
	f := moderationDB(t)
	id := sharedModerationStream(t, f)
	_, owner := connectModerationPublisher(t, f, id, f.userToken)
	_, guest := connectModerationPublisher(t, f, id, f.guestToken)
	if w := f.call("POST", "/streams/"+id+"/comments", f.guestToken, postCommentBody(hex.EncodeToString(randomModerationID(16)), "body")); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	session := hex.EncodeToString(randomModerationID(16))
	prepared := commentService(f, "/internal/comments/"+id+"/prepare", `{"sessionId":"`+session+`"}`)
	if prepared.Code != 200 {
		t.Fatal(prepared.Body.String())
	}
	var publication struct {
		Token     string `json:"token"`
		Namespace string `json:"namespace"`
	}
	json.Unmarshal(prepared.Body.Bytes(), &publication)
	if w := commentService(f, "/relay/authorize", `{"token":"`+publication.Token+`"}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := commentService(f, "/internal/comments/"+id+"/activate", `{"sessionId":"`+session+`"}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	path := "/staff/streams/" + id + "/end"
	body := `{"reason":"PRIVATE force end","publicReason":"This stream violates broadcasting rules"}`
	report := randomModerationID(16)
	reportID := hex.EncodeToString(report)
	streamRaw, _ := streamID(id)
	if _, err := f.db.Exec("INSERT INTO user_reports(id,reporter_id,target_id,reason,incident_at,stream_id,created_at,expires_at) VALUES (?,?,?,?,?,?,?,?)", report, f.user, f.staff, "incident", f.now, streamRaw, f.now, f.now.Add(evidenceRetention)); err != nil {
		t.Fatal(err)
	}
	defer f.db.Exec("DELETE FROM user_reports WHERE id=?", report)
	reportBody, _ := json.Marshal(map[string]string{"reason": "PRIVATE force end", "publicReason": "This stream violates broadcasting rules", "reportId": reportID})
	body = string(reportBody)
	if w := f.call("POST", path, f.staffToken, body); w.Code != 400 {
		t.Fatal("unrelated subject report", w.Code)
	}
	if _, err := f.db.Exec("UPDATE user_reports SET target_id=? WHERE id=?", f.guest, report); err != nil {
		t.Fatal(err)
	}

	if w := f.call("POST", path, f.userToken, body); w.Code != 403 {
		t.Fatal(w.Code)
	}
	trigger := "forced_end_" + hex.EncodeToString(randomModerationID(8))
	if _, err := f.db.Exec(fmt.Sprintf("CREATE TRIGGER %s BEFORE INSERT ON broadcast_permission_actions FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='forced audit failure'", trigger)); err != nil {
		t.Fatal(err)
	}
	defer f.db.Exec("DROP TRIGGER IF EXISTS " + trigger)
	if w := f.call("POST", path, f.staffToken, body); w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(getCommentSnapshot(t, f, id).Comments) != 1 {
		t.Fatal("failure hid comments")
	}
	if w := commentService(f, "/relay/authorize", `{"token":"`+owner+`"}`); w.Code != 200 {
		t.Fatal("rollback revoked grant", w.Code)
	}
	f.db.Exec("DROP TRIGGER " + trigger)
	for i := 0; i < 2; i++ {
		if w := f.call("POST", path, f.staffToken, body); w.Code != 204 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, token := range []string{owner, guest, publication.Token} {
		if w := commentService(f, "/relay/authorize", `{"token":"`+token+`"}`); w.Code != 403 {
			t.Fatal("ended Relay grant", w.Code)
		}
	}
	if w := commentService(f, "/relay/comments/read", `{"namespace":"`+publication.Namespace+`"}`); w.Code != 403 {
		t.Fatal("comment read after end", w.Code)
	}
	if w := f.call("GET", "/public/streams/"+id+"/comments", "", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := f.call("GET", "/public/streams/"+id, "", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := f.call("POST", "/streams/"+id+"/comments", f.userToken, postCommentBody(hex.EncodeToString(randomModerationID(16)), "late")); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := f.call("POST", "/streams/"+id+"/start", f.userToken, "{}"); w.Code != 409 {
		t.Fatal("forced end restarted", w.Code)
	}
	for _, login := range []string{f.userToken, f.guestToken} {
		w := f.call("GET", "/account/streams", login, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "This stream violates broadcasting rules") || strings.Contains(w.Body.String(), "PRIVATE") {
			t.Fatal("participant notice", w.Code, w.Body.String())
		}
	}
	raw, _ := streamID(id)
	var count int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM moderation_events WHERE stream_id=? AND kind='stream_force_ended'", raw).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate end action", count, err)
	}
	if err := f.db.QueryRow("SELECT COUNT(*) FROM moderation_events WHERE stream_id=? AND account_id=? AND report_id=? AND kind='stream_force_ended'", raw, f.guest, report).Scan(&count); err != nil || count != 1 {
		t.Fatal("guest investigation linkage", count, err)
	}

	if err := f.db.QueryRow("SELECT COUNT(*) FROM stream_participants WHERE stream_id=? AND left_at IS NULL", raw).Scan(&count); err != nil || count != 0 {
		t.Fatal("participants retained after end", count, err)
	}
}

func TestBroadcastModerationAuditAndReportsMySQL(t *testing.T) {
	f := moderationDB(t)
	id := sharedModerationStream(t, f)
	_, token := connectModerationPublisher(t, f, id, f.guestToken)
	path := "/staff/accounts/" + hex.EncodeToString(f.guest) + "/broadcast-permission"
	report := randomModerationID(16)
	reportID := hex.EncodeToString(report)
	raw, _ := streamID(id)
	if _, err := f.db.Exec("INSERT INTO user_reports(id,reporter_id,target_id,reason,incident_at,stream_id,created_at,expires_at) VALUES (?,?,?,?,?,?,?,?)", report, f.user, f.guest, "incident", f.now, raw, f.now, f.now.Add(evidenceRetention)); err != nil {
		t.Fatal(err)
	}
	defer f.db.Exec("DELETE FROM user_reports WHERE id=?", report)
	if w := f.call("POST", "/staff/accounts/"+hex.EncodeToString(f.user)+"/broadcast-permission", f.staffToken, broadcastBody(false, reportID)); w.Code != 400 {
		t.Fatal("unrelated report", w.Code)
	}
	trigger := "broadcast_rollback_" + hex.EncodeToString(randomModerationID(8))
	if _, err := f.db.Exec(fmt.Sprintf("CREATE TRIGGER %s BEFORE INSERT ON broadcast_permission_actions FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='forced audit failure'", trigger)); err != nil {
		t.Fatal(err)
	}
	defer f.db.Exec("DROP TRIGGER IF EXISTS " + trigger)
	if w := f.call("POST", path, f.staffToken, broadcastBody(false, reportID)); w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := commentService(f, "/relay/authorize", `{"token":"`+token+`"}`); w.Code != 200 {
		t.Fatal("audit failure revoked grant", w.Code)
	}
	if w := f.call("GET", "/account/broadcast-permission", f.guestToken, ""); !strings.Contains(w.Body.String(), `"broadcastingAllowed":true`) {
		t.Fatal(w.Body.String())
	}
	f.db.Exec("DROP TRIGGER " + trigger)
	if w := f.call("POST", path, f.staffToken, broadcastBody(false, reportID)); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := f.call("POST", "/account/broadcaster", f.guestToken, "{}"); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"suspended"`) {
		t.Fatal("application bypass", w.Code, w.Body.String())
	}
	f.now = f.now.Add(evidenceRetention)
	if err := f.server.cleanupModeration(t.Context()); err != nil {
		t.Fatal(err)
	}
	if w := f.call("GET", "/account/broadcast-permission", f.guestToken, ""); !strings.Contains(w.Body.String(), `"broadcastingAllowed":false`) {
		t.Fatal("history expiry restored permission", w.Body.String())
	}
	if w := f.call("POST", path, f.staffToken, broadcastBody(true, reportID)); w.Code != 400 {
		t.Fatal("expired report", w.Code)
	}
	// Restoration lifts only this restriction, not the underlying approval requirement.
	if _, err := f.db.Exec("UPDATE broadcaster_applications SET approved_at=NULL WHERE account_id=?", f.guest); err != nil {
		t.Fatal(err)
	}
	if w := f.call("POST", path, f.staffToken, broadcastBody(true, "")); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := f.call("GET", "/account/broadcaster", f.guestToken, ""); !strings.Contains(w.Body.String(), `"status":"pending"`) {
		t.Fatal(w.Body.String())
	}
	if w := f.call("POST", "/streams/"+id+"/publishing", f.guestToken, `{"prepare":true,"sessionId":"`+hex.EncodeToString(randomModerationID(16))+`"}`); w.Code != 403 {
		t.Fatal("restoration granted approval", w.Code)
	}
}
