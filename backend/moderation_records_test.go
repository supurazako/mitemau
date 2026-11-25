package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type moderationFixture struct {
	db                                *sql.DB
	server                            *broadcasterServer
	now                               time.Time
	user, guest, staff                []byte
	userToken, guestToken, staffToken string
	mux                               *http.ServeMux
}

func moderationDB(t *testing.T) *moderationFixture {
	t.Helper()
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN not configured")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	f := &moderationFixture{db: db, now: time.Now().UTC().Truncate(time.Microsecond), user: randomModerationID(32), guest: randomModerationID(32), staff: randomModerationID(32)}
	f.userToken = hex.EncodeToString(f.user)
	f.guestToken = hex.EncodeToString(f.guest)
	f.staffToken = hex.EncodeToString(f.staff)
	for _, id := range [][]byte{f.user, f.guest, f.staff} {
		if _, err = db.Exec("INSERT INTO accounts(id,email,created_at) VALUES (?,?,?)", id, hex.EncodeToString(id)+"@moderation.test", f.now); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO account_sessions(token_hash,account_id,expires_at,absolute_expires_at) VALUES (?,?,?,?)", tokenHash(hex.EncodeToString(id)), id, f.now.Add(400*24*time.Hour), f.now.Add(400*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO broadcaster_applications(account_id,requested_at,approved_at) VALUES (?,?,?)", id, f.now, f.now); err != nil {
			t.Fatal(err)
		}
	}
	v := newVerificationServer(db, &recordingSender{}, testConfig())
	v.now = func() time.Time { return f.now }
	f.server = &broadcasterServer{p: &passkeyServer{verification: v}, staffIDs: map[string]bool{hex.EncodeToString(f.staff): true}, relayAuthKey: bytes.Repeat([]byte{0x73}, 32)}
	f.mux = http.NewServeMux()
	f.server.routes(f.mux)
	t.Cleanup(func() {
		for _, id := range [][]byte{f.user, f.guest, f.staff} {
			db.Exec("DELETE FROM moderation_evidence WHERE account_id=?", id)
			db.Exec("DELETE FROM moderation_events WHERE account_id=?", id)
			db.Exec("DELETE FROM moderation_comment_content WHERE account_id=?", id)
			db.Exec("DELETE FROM live_streams WHERE creator_id=?", id)
		}
		for _, id := range [][]byte{f.user, f.guest, f.staff} {
			db.Exec("DELETE FROM accounts WHERE id=?", id)
		}
		db.Close()
	})
	return f
}
func randomModerationID(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}
func (f *moderationFixture) call(method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", f.server.p.verification.config.Origin)
	if token != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	}
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	return w
}
func (f *moderationFixture) transaction(t *testing.T, work func(*sql.Tx) error) {
	t.Helper()
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = work(tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func (f *moderationFixture) path(endpoint string, from, to time.Time) string {
	return "/staff/" + endpoint + "?accountId=" + hex.EncodeToString(f.user) + "&from=" + from.Format(time.RFC3339Nano) + "&to=" + to.Format(time.RFC3339Nano)
}
func readModerationPage(t *testing.T, w *httptest.ResponseRecorder) ([]investigationRecord, *string) {
	t.Helper()
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("investigation caching")
	}
	var page struct {
		Items []investigationRecord `json:"items"`
		Next  *string               `json:"nextCursor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	return page.Items, page.Next
}

func TestModerationRetentionMySQL(t *testing.T) {
	f := moderationDB(t)
	base := f.now
	ctx := context.Background()
	stream := randomModerationID(16)
	comment := randomModerationID(16)
	report := randomModerationID(16)
	var eventID int64
	f.transaction(t, func(tx *sql.Tx) error {
		if err := recordCommentPosted(ctx, tx, f.user, stream, comment, "<script>retained evidence</script>", base); err != nil {
			return err
		}
		if err := tx.QueryRow("SELECT id FROM moderation_events WHERE comment_id=?", comment).Scan(&eventID); err != nil {
			return err
		}
		_, err := recordActivity(ctx, tx, "posting_suspended", f.staff, f.user, stream, nil, report, base)
		return err
	})
	hidden := base.Add(time.Hour)
	accepted := base.Add(2 * time.Hour)
	f.transaction(t, func(tx *sql.Tx) error {
		if err := hideCommentContent(ctx, tx, comment, hidden); err != nil {
			return err
		}
		return retainReportEvidence(ctx, tx, report, eventID, accepted, accepted)
	})
	f.transaction(t, func(tx *sql.Tx) error {
		if err := hideCommentContent(ctx, tx, comment, base.Add(24*time.Hour)); err != nil {
			return err
		}
		return retainReportEvidence(ctx, tx, report, eventID, base.Add(24*time.Hour), base.Add(24*time.Hour))
	})
	var expiry time.Time
	if err := f.db.QueryRow("SELECT expires_at FROM moderation_evidence WHERE report_id=?", report).Scan(&expiry); err != nil || !expiry.Equal(accepted.Add(evidenceRetention)) {
		t.Fatal("retry extended evidence", expiry, err)
	}
	from, to := base.Add(-time.Minute), base.Add(365*24*time.Hour)
	f.now = hidden.Add(commentRetention)
	activity, _ := readModerationPage(t, f.call("GET", f.path("activity", from, to), f.staffToken, ""))
	if len(activity) != 2 {
		t.Fatal("ordinary activity vanished early", len(activity))
	}
	for _, item := range activity {
		if item.Body != nil {
			t.Fatal("expired body exposed before cleanup")
		}
	}
	evidence, _ := readModerationPage(t, f.call("GET", f.path("evidence", from, to), f.staffToken, ""))
	if len(evidence) != 1 || evidence[0].Body == nil || *evidence[0].Body != "<script>retained evidence</script>" {
		t.Fatal("snapshot lost body", evidence)
	}
	if err := f.server.cleanupModeration(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	f.db.QueryRow("SELECT COUNT(*) FROM moderation_comment_content WHERE comment_id=?", comment).Scan(&count)
	if count != 0 {
		t.Fatal("expired body not deleted")
	}
	f.transaction(t, func(tx *sql.Tx) error {
		return retainReportEvidence(ctx, tx, randomModerationID(16), eventID, f.now, f.now)
	})
	var body sql.NullString
	if err := f.db.QueryRow("SELECT body FROM moderation_evidence WHERE report_id<>? AND account_id=?", report, f.user).Scan(&body); err != nil || body.Valid {
		t.Fatal("expired source body recovered", err)
	}
	f.now = base.Add(activityRetention)
	activity, _ = readModerationPage(t, f.call("GET", f.path("activity", from, to), f.staffToken, ""))
	if len(activity) != 1 || activity[0].Kind != "posting_suspended" {
		t.Fatal("90/180 day category", activity)
	}

	if err := f.server.cleanupModeration(ctx); err != nil {
		t.Fatal(err)
	}
	var originalCount int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM moderation_events WHERE id=?", eventID).Scan(&originalCount); err != nil || originalCount != 0 {
		t.Fatal("expired source event survived", originalCount, err)
	}
	var retainedBody string
	if err := f.db.QueryRow("SELECT body FROM moderation_evidence WHERE report_id=?", report).Scan(&retainedBody); err != nil || retainedBody != "<script>retained evidence</script>" {
		t.Fatal("source deletion removed evidence", err)
	}
	f.now = accepted.Add(evidenceRetention)
	activity, _ = readModerationPage(t, f.call("GET", f.path("activity", from, to), f.staffToken, ""))
	if len(activity) != 0 {
		t.Fatal("expired staff history exposed")
	}
	evidence, _ = readModerationPage(t, f.call("GET", f.path("evidence", from, to), f.staffToken, ""))
	for _, item := range evidence {
		if item.ReportID != nil && *item.ReportID == hex.EncodeToString(report) {
			t.Fatal("expired unresolved evidence exposed")
		}
	}
	if err := f.server.cleanupModeration(ctx); err != nil {
		t.Fatal(err)
	}
	f.db.QueryRow("SELECT COUNT(*) FROM moderation_evidence WHERE report_id=?", report).Scan(&count)
	if count != 0 {
		t.Fatal("expired snapshot not deleted")
	}
	f.transaction(t, func(tx *sql.Tx) error {
		if err := retainReportEvidence(ctx, tx, report, eventID, accepted, f.now); err == nil {
			t.Fatal("expired report deadline accepted")
		}
		return nil
	})
	// A still-public comment is not purged because its old posting event expires.
	liveComment := randomModerationID(16)
	f.transaction(t, func(tx *sql.Tx) error {
		return recordCommentPosted(ctx, tx, f.user, stream, liveComment, "still public", f.now)
	})
	f.now = f.now.Add(activityRetention)
	if err := f.server.cleanupModeration(ctx); err != nil {
		t.Fatal(err)
	}
	liveReport := randomModerationID(16)
	f.transaction(t, func(tx *sql.Tx) error { return retainCommentEvidence(ctx, tx, liveReport, liveComment, f.now, f.now) })
	if err := f.db.QueryRow("SELECT body FROM moderation_evidence WHERE report_id=?", liveReport).Scan(&body); err != nil || body.String != "still public" {
		t.Fatal("live comment source lost", err)
	}
}

func TestModerationAccessAndPaginationMySQL(t *testing.T) {
	f := moderationDB(t)
	base := f.now
	stream := randomModerationID(16)
	other := randomModerationID(16)
	f.transaction(t, func(tx *sql.Tx) error {
		for i := 0; i < 52; i++ {
			if _, err := recordActivity(context.Background(), tx, "participant_joined", f.user, f.user, stream, nil, nil, base); err != nil {
				return err
			}
		}
		_, err := recordActivity(context.Background(), tx, "participant_joined", f.user, f.user, other, nil, nil, base)
		return err
	})
	from, to := base.Add(-time.Minute), base.Add(time.Minute)
	for _, endpoint := range []string{"activity", "evidence"} {
		path := f.path(endpoint, from, to)
		for token, want := range map[string]int{"": 401, f.userToken: 403, f.guestToken: 403} {
			if w := f.call("GET", path, token, ""); w.Code != want {
				t.Fatal("nonstaff evidence access", w.Code, want)
			}
		}
		for _, bad := range []string{"accountId=bad", "accountId=" + hex.EncodeToString(f.user), "accountId=" + hex.EncodeToString(f.user) + "&from=" + to.Format(time.RFC3339) + "&to=" + from.Format(time.RFC3339)} {
			if w := f.call("GET", "/staff/"+endpoint+"?"+bad, f.staffToken, ""); w.Code != 400 {
				t.Fatal("invalid filters", w.Code)
			}
		}
	}
	path := f.path("activity", from, to) + "&streamId=" + hex.EncodeToString(stream)
	first, next := readModerationPage(t, f.call("GET", path, f.staffToken, ""))
	if len(first) != 50 || next == nil {
		t.Fatal("page bound", len(first), next)
	}
	second, last := readModerationPage(t, f.call("GET", path+"&before="+*next, f.staffToken, ""))
	if len(second) != 2 || last != nil {
		t.Fatal("older page", len(second), last)
	}
	seen := map[string]bool{}
	for _, item := range append(first, second...) {
		if seen[item.ID] {
			t.Fatal("duplicate page record")
		}
		seen[item.ID] = true
		if item.StreamID == nil || *item.StreamID != hex.EncodeToString(stream) {
			t.Fatal("wrong stream")
		}
	}
	empty, _ := readModerationPage(t, f.call("GET", f.path("activity", to, to.Add(time.Minute)), f.staffToken, ""))
	if len(empty) != 0 {
		t.Fatal("time filter ignored")
	}
	if w := f.call("GET", path+"&before=-1", f.staffToken, ""); w.Code != 400 {
		t.Fatal("invalid cursor accepted")
	}
	delete(f.server.staffIDs, hex.EncodeToString(f.staff))
	if w := f.call("GET", path, f.staffToken, ""); w.Code != 403 {
		t.Fatal("former staff retained access")
	}
}

func TestStreamActivityTransactionsMySQL(t *testing.T) {
	f := moderationDB(t)
	stream := hex.EncodeToString(randomModerationID(16))
	ctx := context.Background()
	must := func(path, token, body string) {
		t.Helper()
		if w := f.call("POST", path, token, body); w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	must("/streams", f.userToken, `{"id":"`+stream+`","title":"Audit stream"}`)
	for i := 0; i < 2; i++ {
		must("/streams/"+stream+"/start", f.userToken, "{}")
	}
	must("/streams/"+stream+"/invite", f.userToken, `{"accountId":"`+hex.EncodeToString(f.guest)+`"}`)
	for i := 0; i < 2; i++ {
		must("/streams/"+stream+"/join", f.guestToken, "{}")
	}
	for i := 0; i < 2; i++ {
		must("/streams/"+stream+"/leave", f.guestToken, "{}")
	}
	must("/streams/"+stream+"/join", f.guestToken, "{}")
	// Whole-stream end hides live comment bodies and records resulting departures.
	comment := randomModerationID(16)
	raw, _ := streamID(stream)
	f.transaction(t, func(tx *sql.Tx) error {
		return recordCommentPosted(ctx, tx, f.guest, raw, comment, "active comment", f.now)
	})
	for i := 0; i < 2; i++ {
		must("/streams/"+stream+"/end", f.userToken, "{}")
	}
	for _, check := range []struct {
		kind string
		want int
	}{{"stream_started", 1}, {"stream_ended", 1}, {"participant_joined", 3}, {"participant_left", 3}} {
		var count int
		if err := f.db.QueryRow("SELECT COUNT(*) FROM moderation_events WHERE stream_id=? AND kind=?", raw, check.kind).Scan(&count); err != nil || count != check.want {
			t.Fatal("idempotent event", check.kind, count, err)
		}
	}
	var hidden sql.NullTime
	if err := f.db.QueryRow("SELECT hidden_at FROM moderation_comment_content WHERE comment_id=?", comment).Scan(&hidden); err != nil || !hidden.Valid {
		t.Fatal("end did not hide comment", err)
	}
	// Failed event recording must also roll back the stream transition.
	second := hex.EncodeToString(randomModerationID(16))
	must("/streams", f.userToken, `{"id":"`+second+`","title":"Rollback"}`)
	trigger := "moderation_fail_" + second
	if _, err := f.db.Exec(fmt.Sprintf("CREATE TRIGGER %s BEFORE INSERT ON moderation_events FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='forced audit failure'", trigger)); err != nil {
		t.Fatal(err)
	}
	defer f.db.Exec("DROP TRIGGER IF EXISTS " + trigger)
	if w := f.call("POST", "/streams/"+second+"/start", f.userToken, "{}"); w.Code != 503 {
		t.Fatal("event failure not reported", w.Code)
	}
	var state string
	rawSecond, _ := streamID(second)
	if err := f.db.QueryRow("SELECT state FROM live_streams WHERE id=?", rawSecond).Scan(&state); err != nil || state != "ready" {
		t.Fatal("partial transition committed", state, err)
	}
}

func TestApprovalActivityMySQL(t *testing.T) {
	f := moderationDB(t)
	if _, err := f.db.Exec("UPDATE broadcaster_applications SET approved_at=NULL,approved_by=NULL WHERE account_id=?", f.user); err != nil {
		t.Fatal(err)
	}
	path := "/staff/broadcasters/" + hex.EncodeToString(f.user) + "/approve"
	if w := f.call("POST", path, f.staffToken, "{}"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := f.call("POST", path, f.staffToken, "{}"); w.Code != 409 {
		t.Fatal("duplicate approval", w.Code)
	}
	var count int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM moderation_events WHERE account_id=? AND actor_id=? AND kind='broadcaster_approved' AND expires_at=?", f.user, f.staff, f.now.Add(evidenceRetention)).Scan(&count); err != nil || count != 1 {
		t.Fatal("approval audit", count, err)
	}
	actorPath := f.path("activity", f.now.Add(-time.Minute), f.now.Add(time.Minute))
	actorPath = strings.Replace(actorPath, hex.EncodeToString(f.user), hex.EncodeToString(f.staff), 1)
	events, _ := readModerationPage(t, f.call("GET", actorPath, f.staffToken, ""))
	if len(events) != 1 || events[0].AccountID != hex.EncodeToString(f.user) {
		t.Fatal("actor activity search omitted action", events)
	}

}
