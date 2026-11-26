package main

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func createCommentStream(t *testing.T, f *moderationFixture) string {
	t.Helper()
	id := hex.EncodeToString(randomModerationID(16))
	if w := f.call("POST", "/streams", f.userToken, `{"id":"`+id+`","title":"Comments"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := f.call("POST", "/streams/"+id+"/start", f.userToken, "{}"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	return id
}
func postCommentBody(id, body string) string {
	data, _ := json.Marshal(map[string]string{"requestId": id, "body": body})
	return string(data)
}
func getCommentSnapshot(t *testing.T, f *moderationFixture, id string) commentSnapshot {
	t.Helper()
	w := f.call("GET", "/public/streams/"+id+"/comments", "", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("comment caching")
	}
	var snapshot commentSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}
func commentService(f *moderationFixture, path, body string) *httptest.ResponseRecorder {
	method := "POST"
	if body == "" {
		method = "GET"
	}
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+hex.EncodeToString(f.server.relayAuthKey))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	return w
}
func TestLiveCommentsMySQL(t *testing.T) {
	f := moderationDB(t)
	stream := createCommentStream(t, f)
	other := createCommentStream(t, f)
	if _, err := f.db.Exec("UPDATE broadcaster_applications SET approved_at=NULL WHERE account_id=?", f.guest); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 55; i++ {
		id := hex.EncodeToString(randomModerationID(16))
		body := fmt.Sprintf("Comment %02d <script>plain text</script>", i)
		if w := f.call("POST", "/streams/"+stream+"/comments", f.guestToken, postCommentBody(id, body)); w.Code != 200 {
			t.Fatal("signed-in non-broadcaster comment", w.Code, w.Body.String())
		}
		f.now = f.now.Add(commentInterval)
	}
	snapshot := getCommentSnapshot(t, f, stream)
	if snapshot.StreamID != stream || snapshot.Version != "55" || len(snapshot.Comments) != 50 || !strings.HasPrefix(snapshot.Comments[0].Body, "Comment 05") || !strings.HasPrefix(snapshot.Comments[49].Body, "Comment 54") {
		t.Fatal("latest 50/order/version", snapshot)
	}
	if len(getCommentSnapshot(t, f, other).Comments) != 0 {
		t.Fatal("cross-stream comments")
	}
	raw, _ := streamID(stream)
	var recorded int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM moderation_events WHERE stream_id=? AND kind='comment_posted'", raw).Scan(&recorded); err != nil || recorded != 55 {
		t.Fatal("missing posting records", recorded, err)
	}
	if w := f.call("POST", "/streams/"+stream+"/end", f.userToken, "{}"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, path := range []string{"/public/streams/" + stream + "/comments", "/public/streams/" + stream + "/comments/channel"} {
		if w := f.call("GET", path, "", ""); w.Code != 404 {
			t.Fatal("ended history public", w.Code, w.Body.String())
		}
	}
	if w := f.call("POST", "/streams/"+stream+"/comments", f.guestToken, postCommentBody(snapshot.Comments[0].ID, snapshot.Comments[0].Body)); w.Code != 409 {
		t.Fatal("ended retry disclosed history", w.Code)
	}
	var hidden int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM moderation_comment_content WHERE stream_id=? AND hidden_at IS NOT NULL AND expires_at=?", raw, f.now.Add(commentRetention)).Scan(&hidden); err != nil || hidden != 55 {
		t.Fatal("end retention", hidden, err)
	}
}
func TestCommentLimitsAndRetriesMySQL(t *testing.T) {
	f := moderationDB(t)
	stream := createCommentStream(t, f)
	other := createCommentStream(t, f)
	path := "/streams/" + stream + "/comments"
	id := hex.EncodeToString(randomModerationID(16))
	if w := f.call("POST", path, "", postCommentBody(id, "hello")); w.Code != 401 {
		t.Fatal("anonymous posting", w.Code)
	}
	for _, body := range []string{" \n ", strings.Repeat("😀", 501)} {
		if w := f.call("POST", path, f.guestToken, postCommentBody(id, body)); w.Code != 400 {
			t.Fatal("input limit", w.Code)
		}
	}
	body := strings.Repeat("😀", 500)
	if w := f.call("POST", path, f.guestToken, postCommentBody(id, body)); w.Code != 200 {
		t.Fatal("Unicode limit", w.Code, w.Body.String())
	}
	if w := f.call("POST", path, f.guestToken, postCommentBody(id, " "+body+" ")); w.Code != 200 {
		t.Fatal("idempotent retry", w.Code)
	}
	if w := f.call("POST", path, f.guestToken, postCommentBody(id, "different")); w.Code != 409 {
		t.Fatal("identity reuse", w.Code)
	}
	for _, endpoint := range []string{path, "/streams/" + other + "/comments"} {
		if w := f.call("POST", endpoint, f.guestToken, postCommentBody(hex.EncodeToString(randomModerationID(16)), "too soon")); w.Code != 429 || w.Header().Get("Retry-After") == "" {
			t.Fatal("global cooldown", w.Code)
		}
	}
	renewed := hex.EncodeToString(randomModerationID(32))
	if _, err := f.db.Exec("INSERT INTO account_sessions(token_hash,account_id,expires_at,absolute_expires_at) VALUES (?,?,?,?)", tokenHash(renewed), f.guest, f.now.Add(time.Hour), f.now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if w := f.call("POST", path, renewed, postCommentBody(hex.EncodeToString(randomModerationID(16)), "new login")); w.Code != 429 {
		t.Fatal("session bypassed cooldown", w.Code)
	}
	f.now = f.now.Add(commentInterval)
	if _, err := f.db.Exec("INSERT INTO account_comment_permissions(account_id,posting_allowed) VALUES (?,FALSE)", f.guest); err != nil {
		t.Fatal(err)
	}
	if w := f.call("POST", path, renewed, postCommentBody(hex.EncodeToString(randomModerationID(16)), "blocked")); w.Code != 403 {
		t.Fatal("existing-session restriction ignored", w.Code)
	}
	if _, err := f.db.Exec("UPDATE account_comment_permissions SET posting_allowed=TRUE WHERE account_id=?", f.guest); err != nil {
		t.Fatal(err)
	}
	if w := f.call("POST", path, renewed, postCommentBody(hex.EncodeToString(randomModerationID(16)), "restored")); w.Code != 200 {
		t.Fatal("exact cooldown boundary/restoration", w.Code)
	}
	if len(getCommentSnapshot(t, f, stream).Comments) != 2 {
		t.Fatal("retry duplicated comment or invalid request posted")
	}
}
func TestConcurrentCommentCooldownMySQL(t *testing.T) {
	f := moderationDB(t)
	first, second := createCommentStream(t, f), createCommentStream(t, f)
	statuses := make(chan int, 2)
	for _, stream := range []string{first, second} {
		go func(id string) {
			statuses <- f.call("POST", "/streams/"+id+"/comments", f.guestToken, postCommentBody(hex.EncodeToString(randomModerationID(16)), "concurrent")).Code
		}(stream)
	}
	got := map[int]int{}
	got[<-statuses]++
	got[<-statuses]++
	if got[200] != 1 || got[429] != 1 {
		t.Fatal("concurrent account cooldown", got)
	}
}
func TestCommentPublisherAuthorizationMySQL(t *testing.T) {
	f := moderationDB(t)
	stream := createCommentStream(t, f)
	session := hex.EncodeToString(randomModerationID(16))
	body := `{"sessionId":"` + session + `"}`
	endpoint := "/internal/comments/" + stream
	if w := f.call("POST", endpoint+"/prepare", f.userToken, body); w.Code != 401 {
		t.Fatal("browser obtained server credential", w.Code)
	}
	w := commentService(f, endpoint+"/prepare", body)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var grant map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &grant); err != nil {
		t.Fatal(err)
	}
	if getCommentSnapshot(t, f, stream).Namespace != nil {
		t.Fatal("prepared channel advertised")
	}
	if w := commentService(f, endpoint+"/activate", body); w.Code != 409 {
		t.Fatal("unconnected channel activated", w.Code)
	}
	authorize := func(token string) *httptest.ResponseRecorder {
		return commentService(f, "/relay/authorize", `{"token":"`+token+`"}`)
	}
	if w := authorize(grant["token"]); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := commentService(f, endpoint+"/activate", body); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if getCommentSnapshot(t, f, stream).Namespace == nil {
		t.Fatal("active comment channel unavailable")
	}
	read := func(namespace string) *httptest.ResponseRecorder {
		return commentService(f, "/relay/comments/read", `{"namespace":"`+namespace+`"}`)
	}
	if w := read(grant["namespace"]); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// The server token cannot make a media namespace: the returned scope is exact.
	var permission map[string]any
	json.Unmarshal(authorize(grant["token"]).Body.Bytes(), &permission)
	if permission["namespace"] != grant["namespace"] {
		t.Fatal("unexpected comment scope")
	}
	second := hex.EncodeToString(randomModerationID(16))
	nextBody := `{"sessionId":"` + second + `"}`
	if w := commentService(f, endpoint+"/prepare", nextBody); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := authorize(grant["token"]); w.Code != 403 {
		t.Fatal("old publisher survived replacement", w.Code)
	}
	if w := read(grant["namespace"]); w.Code != 403 {
		t.Fatal("old cache channel still readable", w.Code)
	}
	if w := commentService(f, endpoint+"/activate", body); w.Code != 409 {
		t.Fatal("late activation replaced new publisher", w.Code)
	}
	nextNamespace := commentNamespace(mustCommentID(stream), mustCommentID(second))
	nextToken := f.server.publisherCredential(nextNamespace)
	if w := authorize(nextToken); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := commentService(f, endpoint+"/activate", nextBody); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	f.now = f.now.Add(publisherLease)
	if w := authorize(nextToken); w.Code != 403 {
		t.Fatal("expired server grant revived", w.Code)
	}
	if getCommentSnapshot(t, f, stream).Namespace != nil {
		t.Fatal("stale publisher advertised")
	}
	if w := read(nextNamespace); w.Code != 403 {
		t.Fatal("stale publisher readable", w.Code)
	}
	if w := f.call("POST", "/streams/"+stream+"/end", f.userToken, "{}"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := commentService(f, endpoint+"/prepare", `{"sessionId":"`+hex.EncodeToString(randomModerationID(16))+`"}`); w.Code != 409 {
		t.Fatal("ended publisher prepared", w.Code)
	}
	if w := commentService(f, endpoint, ""); w.Code != 404 {
		t.Fatal("internal ended history exposed", w.Code)
	}
}
func mustCommentID(id string) []byte { raw, _ := streamID(id); return raw }
func TestCommentWriteRollbackMySQL(t *testing.T) {
	f := moderationDB(t)
	stream := createCommentStream(t, f)
	id := hex.EncodeToString(randomModerationID(16))
	trigger := "comment_fail_" + stream
	if _, err := f.db.Exec(fmt.Sprintf("CREATE TRIGGER %s BEFORE INSERT ON moderation_comment_content FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='forced content failure'", trigger)); err != nil {
		t.Fatal(err)
	}
	defer f.db.Exec("DROP TRIGGER IF EXISTS " + trigger)
	if w := f.call("POST", "/streams/"+stream+"/comments", f.guestToken, postCommentBody(id, "rollback")); w.Code != 503 {
		t.Fatal(w.Code)
	}
	if len(getCommentSnapshot(t, f, stream).Comments) != 0 {
		t.Fatal("partial comment committed")
	}
	var count int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM comment_posting_cooldowns WHERE account_id=?", f.guest).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed post consumed cooldown", count, err)
	}
	if _, err := f.db.Exec("DROP TRIGGER " + trigger); err != nil {
		t.Fatal(err)
	}
	if w := f.call("POST", "/streams/"+stream+"/comments", f.guestToken, postCommentBody(id, "rollback")); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	snapshot := getCommentSnapshot(t, f, stream)
	if snapshot.Version != "1" || len(snapshot.Comments) != 1 {
		t.Fatal("rollback version", snapshot.Version)
	}
	// Hiding is independent of public metadata and makes future #53 deletion invisible.
	raw := mustCommentID(snapshot.Comments[0].ID)
	f.transaction(t, func(tx *sql.Tx) error { return hideCommentContent(context.Background(), tx, raw, f.now) })
	if len(getCommentSnapshot(t, f, stream).Comments) != 0 {
		t.Fatal("hidden content exposed")
	}
}

func TestReusedExpiredCommentRequestCannotRewriteAuditMySQL(t *testing.T) {
	f := moderationDB(t)
	stream := createCommentStream(t, f)
	request := hex.EncodeToString(randomModerationID(16))
	post := func(stream, body string) string {
		t.Helper()
		w := f.call("POST", "/streams/"+stream+"/comments", f.guestToken, postCommentBody(request, body))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var response map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response["id"]
	}
	original := post(stream, "original")
	if original == request {
		t.Fatal("untrusted request ID became the public comment ID")
	}
	if w := f.call("POST", "/streams/"+stream+"/end", f.userToken, "{}"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	f.now = f.now.Add(commentRetention)
	if err := f.server.cleanupModeration(context.Background()); err != nil {
		t.Fatal(err)
	}
	other := createCommentStream(t, f)
	replacement := post(other, "replacement")
	if replacement == original {
		t.Fatal("expired identity reused")
	}
	var leaked int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM moderation_events e JOIN moderation_comment_content c ON c.comment_id=e.comment_id WHERE e.comment_id=?`, mustCommentID(original)).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("old audit record acquired new content", leaked, err)
	}
}
