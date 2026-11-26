package main

import (
	"database/sql"
	"encoding/hex"
	"net/http"
	"strconv"
	"time"
)

type investigationRecord struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	ActorID    string    `json:"actorId"`
	AccountID  string    `json:"accountId"`
	StreamID   *string   `json:"streamId"`
	CommentID  *string   `json:"commentId"`
	ReportID   *string   `json:"reportId"`
	OccurredAt time.Time `json:"occurredAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	Body       *string   `json:"body"`
}

func (s *broadcasterServer) moderationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /staff/activity", s.activityRecords)
	mux.HandleFunc("GET /staff/evidence", s.evidenceRecords)
}
func (s *broadcasterServer) activityRecords(w http.ResponseWriter, r *http.Request) {
	s.investigation(w, r, false)
}
func (s *broadcasterServer) evidenceRecords(w http.ResponseWriter, r *http.Request) {
	s.investigation(w, r, true)
}

func (s *broadcasterServer) investigation(w http.ResponseWriter, r *http.Request, evidence bool) {
	w.Header().Set("Cache-Control", "no-store")
	if _, ok := s.authenticate(w, r, true); !ok {
		return
	}
	q := r.URL.Query()
	account, err := hex.DecodeString(q.Get("accountId"))
	if err != nil || len(account) != 32 {
		problem(w, 400, "Provide a valid account ID")
		return
	}
	from, err := time.Parse(time.RFC3339Nano, q.Get("from"))
	if err != nil {
		problem(w, 400, "Provide a valid start time")
		return
	}
	to, err := time.Parse(time.RFC3339Nano, q.Get("to"))
	if err != nil || !to.After(from) {
		problem(w, 400, "Provide a later end time")
		return
	}
	var stream []byte
	if q.Get("streamId") != "" {
		stream, err = streamID(q.Get("streamId"))
		if err != nil {
			problem(w, 400, "Invalid stream ID")
			return
		}
	}
	before := int64(0)
	if q.Get("before") != "" {
		before, err = strconv.ParseInt(q.Get("before"), 10, 64)
		if err != nil || before <= 0 {
			problem(w, 400, "Invalid cursor")
			return
		}
	}
	now := s.p.verification.now()
	selectSQL := `SELECT e.id,e.kind,e.actor_id,e.account_id,e.stream_id,e.comment_id,e.report_id,e.occurred_at,e.expires_at,COALESCE(o.outcome,c.body) FROM moderation_events e LEFT JOIN moderation_comment_content c ON c.comment_id=e.comment_id AND (c.expires_at IS NULL OR c.expires_at>?) LEFT JOIN report_outcomes o ON e.kind='report_resolved' AND o.report_id=e.report_id AND o.expires_at>?`
	args := []any{now, now}
	if evidence {
		selectSQL = `SELECT e.id,e.kind,e.actor_id,e.account_id,e.stream_id,e.comment_id,e.report_id,e.occurred_at,e.expires_at,e.body FROM moderation_evidence e`
		args = nil
	}
	selectSQL += ` WHERE (e.account_id=? OR e.actor_id=?) AND e.occurred_at>=? AND e.occurred_at<? AND e.expires_at>?`
	args = append(args, account, account, from.UTC(), to.UTC(), now)
	if stream != nil {
		selectSQL += " AND e.stream_id=?"
		args = append(args, stream)
	}
	if before > 0 {
		selectSQL += " AND e.id<?"
		args = append(args, before)
	}
	selectSQL += " ORDER BY e.id DESC LIMIT 51"
	rows, err := s.p.verification.db.QueryContext(r.Context(), selectSQL, args...)
	if err != nil {
		problem(w, 503, "Could not load investigation records")
		return
	}
	defer rows.Close()
	items := []investigationRecord{}
	for rows.Next() {
		var item investigationRecord
		var id int64
		var actor, subject, stream, comment, report []byte
		var body sql.NullString
		if err = rows.Scan(&id, &item.Kind, &actor, &subject, &stream, &comment, &report, &item.OccurredAt, &item.ExpiresAt, &body); err != nil {
			problem(w, 503, "Could not load investigation records")
			return
		}
		item.ID = strconv.FormatInt(id, 10)
		item.ActorID = hex.EncodeToString(actor)
		item.AccountID = hex.EncodeToString(subject)
		item.StreamID = optionalHex(stream)
		item.CommentID = optionalHex(comment)
		item.ReportID = optionalHex(report)
		if body.Valid {
			item.Body = &body.String
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		problem(w, 503, "Could not load investigation records")
		return
	}
	var next *string
	if len(items) > 50 {
		items = items[:50]
		cursor := items[49].ID
		next = &cursor
	}
	writeJSON(w, 200, map[string]any{"items": items, "nextCursor": next})
}
func optionalHex(raw []byte) *string {
	if raw == nil {
		return nil
	}
	value := hex.EncodeToString(raw)
	return &value
}
