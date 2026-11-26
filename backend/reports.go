package main

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func (s *broadcasterServer) reportRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /reports", s.p.verification.protect(s.createReport))
	mux.HandleFunc("GET /staff/reports", s.listReports)
	mux.HandleFunc("GET /staff/reports/{id}/evidence", s.reportEvidence)
	mux.HandleFunc("POST /staff/reports/{id}/delete-comment", s.p.verification.protect(s.staffDeleteComment))
	mux.HandleFunc("POST /staff/reports/{id}/resolve", s.p.verification.protect(s.resolveReport))
}
func (s *broadcasterServer) createReport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	var input struct {
		TargetID   string `json:"targetId"`
		Reason     string `json:"reason"`
		IncidentAt string `json:"incidentAt"`
		StreamID   string `json:"streamId"`
		CommentID  string `json:"commentId"`
	}
	if !decodeBodyLimit(w, r, &input, 8192) {
		return
	}
	target, err := hex.DecodeString(input.TargetID)
	reason := strings.TrimSpace(input.Reason)
	incident, parseErr := time.Parse(time.RFC3339Nano, input.IncidentAt)
	now := s.p.verification.now()
	if err != nil || len(target) != 32 || bytes.Equal(target, actor) || !utf8.ValidString(reason) || utf8.RuneCountInString(reason) < 1 || utf8.RuneCountInString(reason) > 2000 || parseErr != nil || incident.After(now) {
		problem(w, 400, "Provide another account, a reason and a valid incident time")
		return
	}
	var stream, comment []byte
	if input.StreamID != "" {
		stream, err = streamID(input.StreamID)
		if err != nil {
			problem(w, 400, "Invalid stream ID")
			return
		}
	}
	if input.CommentID != "" {
		comment, err = streamID(input.CommentID)
		if err != nil {
			problem(w, 400, "Invalid comment ID")
			return
		}
	}
	tx, err := s.p.verification.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Could not submit report")
		return
	}
	defer tx.Rollback()
	var locked []byte
	if err = tx.QueryRowContext(r.Context(), "SELECT id FROM accounts WHERE id=? FOR UPDATE", actor).Scan(&locked); err != nil {
		problem(w, 503, "Could not submit report")
		return
	}
	var exists int
	if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM accounts WHERE id=?", target).Scan(&exists); err != nil {
		problem(w, 503, "Could not submit report")
		return
	}
	if exists == 0 {
		problem(w, 404, "Account not found")
		return
	}
	var next time.Time
	err = tx.QueryRowContext(r.Context(), "SELECT next_report_at FROM report_cooldowns WHERE account_id=?", actor).Scan(&next)
	if err != nil && err != sql.ErrNoRows {
		problem(w, 503, "Could not submit report")
		return
	}
	now = s.p.verification.now()
	if now.Before(next) {
		w.Header().Set("Retry-After", "60")
		problem(w, 429, "Wait before submitting another report")
		return
	}
	if comment != nil {
		var owner, source []byte
		err = tx.QueryRowContext(r.Context(), `SELECT account_id,stream_id FROM moderation_comment_content WHERE comment_id=? AND (expires_at IS NULL OR expires_at>?) FOR UPDATE`, comment, now).Scan(&owner, &source)
		if err == sql.ErrNoRows || (err == nil && (!bytes.Equal(owner, target) || (stream != nil && !bytes.Equal(stream, source)))) {
			problem(w, 400, "Comment does not match the reported account and stream")
			return
		}
		if err != nil {
			problem(w, 503, "Could not submit report")
			return
		}
		stream = source
	}
	if stream != nil {
		if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM live_streams WHERE id=?", stream).Scan(&exists); err != nil {
			problem(w, 503, "Could not submit report")
			return
		}
		if exists == 0 {
			problem(w, 400, "Stream not found")
			return
		}
	}
	id := make([]byte, 16)
	if _, err = rand.Read(id); err != nil {
		problem(w, 503, "Could not submit report")
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO user_reports(id,reporter_id,target_id,reason,incident_at,stream_id,comment_id,created_at,expires_at) VALUES (?,?,?,?,?,?,?,?,?)`, id, actor, target, reason, incident.UTC(), stream, comment, now, now.Add(evidenceRetention))
	if err == nil && comment != nil {
		err = retainCommentEvidence(r.Context(), tx, id, comment, now, now)
	}
	if err == nil {
		_, err = recordActivity(r.Context(), tx, "report_created", actor, target, stream, comment, id, now)
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO report_cooldowns(account_id,next_report_at) VALUES (?,?) ON DUPLICATE KEY UPDATE next_report_at=VALUES(next_report_at)`, actor, now.Add(time.Minute))
	}
	if err != nil {
		problem(w, 503, "Could not submit report")
		return
	}
	if err = tx.Commit(); err != nil {
		problem(w, 503, "Could not submit report")
		return
	}
	writeJSON(w, 200, map[string]string{"id": hex.EncodeToString(id)})
}

type reportItem struct {
	ID         string    `json:"id"`
	ReporterID string    `json:"reporterId"`
	TargetID   string    `json:"targetId"`
	Reason     string    `json:"reason"`
	IncidentAt time.Time `json:"incidentAt"`
	StreamID   *string   `json:"streamId"`
	CommentID  *string   `json:"commentId"`
	State      string    `json:"state"`
	Outcome    *string   `json:"outcome"`
	CreatedAt  time.Time `json:"createdAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

func (s *broadcasterServer) listReports(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if _, ok := s.authenticate(w, r, true); !ok {
		return
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		var err error
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 || offset > 1000000 {
			problem(w, 400, "Invalid offset")
			return
		}
	}
	rows, err := s.p.verification.db.QueryContext(r.Context(), `SELECT id,reporter_id,target_id,reason,incident_at,stream_id,comment_id,state,outcome,created_at,expires_at FROM user_reports WHERE expires_at>? ORDER BY state,created_at DESC,id DESC LIMIT 100 OFFSET ?`, s.p.verification.now(), offset)
	if err != nil {
		problem(w, 503, "Could not load reports")
		return
	}
	defer rows.Close()
	items := []reportItem{}
	for rows.Next() {
		var item reportItem
		var id, reporter, target, stream, comment []byte
		var outcome sql.NullString
		if err = rows.Scan(&id, &reporter, &target, &item.Reason, &item.IncidentAt, &stream, &comment, &item.State, &outcome, &item.CreatedAt, &item.ExpiresAt); err != nil {
			problem(w, 503, "Could not load reports")
			return
		}
		item.ID = hex.EncodeToString(id)
		item.ReporterID = hex.EncodeToString(reporter)
		item.TargetID = hex.EncodeToString(target)
		item.StreamID = optionalHex(stream)
		item.CommentID = optionalHex(comment)
		if outcome.Valid {
			item.Outcome = &outcome.String
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		problem(w, 503, "Could not load reports")
		return
	}
	writeJSON(w, 200, items)
}
func (s *broadcasterServer) resolveReport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, ok := s.authenticate(w, r, true)
	if !ok {
		return
	}
	id, err := streamID(r.PathValue("id"))
	if err != nil {
		problem(w, 400, "Invalid report ID")
		return
	}
	var input struct {
		Outcome string `json:"outcome"`
	}
	if !decodeBodyLimit(w, r, &input, 8192) {
		return
	}
	outcome := strings.TrimSpace(input.Outcome)
	if !utf8.ValidString(outcome) || utf8.RuneCountInString(outcome) < 1 || utf8.RuneCountInString(outcome) > 2000 {
		problem(w, 400, "Provide an outcome")
		return
	}
	tx, err := s.p.verification.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Could not resolve report")
		return
	}
	defer tx.Rollback()
	var target, stream, comment []byte
	var state string
	now := s.p.verification.now()
	err = tx.QueryRowContext(r.Context(), "SELECT target_id,stream_id,comment_id,state FROM user_reports WHERE id=? AND expires_at>? FOR UPDATE", id, now).Scan(&target, &stream, &comment, &state)
	if err == sql.ErrNoRows {
		problem(w, 404, "Report not found")
		return
	}
	if err != nil {
		problem(w, 503, "Could not resolve report")
		return
	}
	if state != "open" {
		problem(w, 409, "Report already resolved")
		return
	}
	_, err = tx.ExecContext(r.Context(), "UPDATE user_reports SET state='resolved',outcome=?,resolved_by=?,resolved_at=? WHERE id=?", outcome, actor, now, id)
	if err == nil {
		_, err = tx.ExecContext(r.Context(), "INSERT INTO report_outcomes(report_id,target_id,actor_id,outcome,occurred_at,expires_at) VALUES (?,?,?,?,?,?)", id, target, actor, outcome, now, now.Add(evidenceRetention))
	}
	if err == nil {
		_, err = recordActivity(r.Context(), tx, "report_resolved", actor, target, stream, comment, id, now)
	}
	if err != nil {
		problem(w, 503, "Could not resolve report")
		return
	}
	if tx.Commit() != nil {
		problem(w, 503, "Could not resolve report")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *broadcasterServer) staffDeleteComment(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, ok := s.authenticate(w, r, true)
	if !ok {
		return
	}
	id, err := streamID(r.PathValue("id"))
	if err != nil {
		problem(w, 400, "Invalid report ID")
		return
	}
	var input struct {
		CommentID string `json:"commentId"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	var requested []byte
	if input.CommentID != "" {
		requested, err = streamID(input.CommentID)
		if err != nil {
			problem(w, 400, "Invalid comment ID")
			return
		}
	}
	tx, err := s.p.verification.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Could not delete comment")
		return
	}
	defer tx.Rollback()
	now := s.p.verification.now()
	var target, stream, comment []byte
	var accepted time.Time
	err = tx.QueryRowContext(r.Context(), "SELECT target_id,stream_id,comment_id,created_at FROM user_reports WHERE id=? AND expires_at>? FOR UPDATE", id, now).Scan(&target, &stream, &comment, &accepted)
	if err == sql.ErrNoRows {
		problem(w, 404, "Report not found")
		return
	}
	if err != nil {
		problem(w, 503, "Could not delete comment")
		return
	}
	if requested != nil {
		comment = requested
		err = tx.QueryRowContext(r.Context(), "SELECT stream_id FROM moderation_comment_content WHERE comment_id=? AND account_id=?", comment, target).Scan(&stream)
		if err == sql.ErrNoRows {
			problem(w, 404, "Comment not found for reported user")
			return
		}
		if err != nil {
			problem(w, 503, "Could not delete comment")
			return
		}
	}
	if comment == nil || stream == nil {
		problem(w, 400, "Report has no linked comment")
		return
	}
	var state string
	err = tx.QueryRowContext(r.Context(), "SELECT state FROM live_streams WHERE id=? FOR UPDATE", stream).Scan(&state)
	if err != nil {
		problem(w, 503, "Could not delete comment")
		return
	}
	var hidden sql.NullTime
	err = tx.QueryRowContext(r.Context(), "SELECT hidden_at FROM moderation_comment_content WHERE comment_id=? AND account_id=? FOR UPDATE", comment, target).Scan(&hidden)
	if err == sql.ErrNoRows {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		problem(w, 503, "Could not delete comment")
		return
	}
	err = retainCommentEvidence(r.Context(), tx, id, comment, accepted, now)
	if err != nil {
		problem(w, 503, "Could not retain evidence")
		return
	}
	if !hidden.Valid {
		if err == nil {
			err = hideCommentContent(r.Context(), tx, comment, now)
		}
		if err == nil {
			_, err = recordActivity(r.Context(), tx, "staff_comment_deleted", actor, target, stream, comment, id, now)
		}
		if err == nil && state == "live" {
			_, err = tx.ExecContext(r.Context(), "UPDATE live_streams SET comment_version=comment_version+1 WHERE id=?", stream)
		}
		if err != nil {
			problem(w, 503, "Could not delete comment")
			return
		}
	}
	if tx.Commit() != nil {
		problem(w, 503, "Could not delete comment")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *broadcasterServer) reportEvidence(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if _, ok := s.authenticate(w, r, true); !ok {
		return
	}
	id, err := streamID(r.PathValue("id"))
	if err != nil {
		problem(w, 400, "Invalid report ID")
		return
	}
	var count int
	now := s.p.verification.now()
	if err = s.p.verification.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM user_reports WHERE id=? AND expires_at>?", id, now).Scan(&count); err != nil {
		problem(w, 503, "Could not load evidence")
		return
	}
	if count == 0 {
		problem(w, 404, "Report not found")
		return
	}
	rows, err := s.p.verification.db.QueryContext(r.Context(), "SELECT kind,body,occurred_at FROM moderation_evidence WHERE report_id=? AND expires_at>? ORDER BY id", id, now)
	if err != nil {
		problem(w, 503, "Could not load evidence")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var kind string
		var body sql.NullString
		var at time.Time
		if rows.Scan(&kind, &body, &at) != nil {
			problem(w, 503, "Could not load evidence")
			return
		}
		var value any
		if body.Valid {
			value = body.String
		}
		items = append(items, map[string]any{"kind": kind, "body": value, "occurredAt": at})
	}
	if rows.Err() != nil {
		problem(w, 503, "Could not load evidence")
		return
	}
	writeJSON(w, 200, items)
}
