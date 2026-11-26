package main

import (
	"bytes"
	"database/sql"
	"net/http"
	"strings"
)

func (s *broadcasterServer) forceEndStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, ok := s.authenticate(w, r, true)
	if !ok {
		return
	}
	id, err := streamID(r.PathValue("id"))
	if err != nil {
		problem(w, 400, "Invalid stream ID")
		return
	}
	var input struct {
		Reason       string `json:"reason"`
		PublicReason string `json:"publicReason"`
		ReportID     string `json:"reportId"`
	}
	if !decodeBodyLimit(w, r, &input, 16384) {
		return
	}
	reason, publicReason := strings.TrimSpace(input.Reason), strings.TrimSpace(input.PublicReason)
	if !validRestrictionText(reason, 2000) || !validRestrictionText(publicReason, 500) {
		problem(w, 400, "Provide separate staff and participant-facing reasons")
		return
	}
	var report []byte
	if input.ReportID != "" {
		report, err = streamID(input.ReportID)
		if err != nil {
			problem(w, 400, "Invalid report ID")
			return
		}
	}
	tx, err := s.p.verification.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Could not end stream")
		return
	}
	defer tx.Rollback()
	now := s.p.verification.now()
	var reportTarget, reportStream []byte
	if report != nil {
		err = tx.QueryRowContext(r.Context(), "SELECT target_id,stream_id FROM user_reports WHERE id=? AND expires_at>? FOR UPDATE", report, now).Scan(&reportTarget, &reportStream)
		if err == sql.ErrNoRows {
			problem(w, 400, "Report has expired or does not exist")
			return
		}
		if err != nil {
			problem(w, 503, "Could not check report")
			return
		}
	}
	var creator []byte
	var state string
	err = tx.QueryRowContext(r.Context(), "SELECT creator_id,state FROM live_streams WHERE id=? FOR UPDATE", id).Scan(&creator, &state)
	if err == sql.ErrNoRows {
		problem(w, 404, "Stream not found")
		return
	}
	if err != nil {
		problem(w, 503, "Could not end stream")
		return
	}
	if report != nil {
		var involved int
		err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM stream_participants WHERE stream_id=? AND account_id=?", id, reportTarget).Scan(&involved)
		if err != nil {
			problem(w, 503, "Could not check report")
			return
		}
		if (!bytes.Equal(creator, reportTarget) && involved == 0) || (reportStream != nil && !bytes.Equal(reportStream, id)) {
			problem(w, 400, "Report does not relate to this stream")
			return
		}
	}
	if state == "ended" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	subject := creator
	if reportTarget != nil {
		subject = reportTarget
	}
	event, err := finishStream(r.Context(), tx, id, actor, subject, report, "stream_force_ended", now)
	if err == nil {
		_, err = tx.ExecContext(r.Context(), "INSERT INTO broadcast_permission_actions(event_id,reason,public_reason,expires_at) VALUES (?,?,?,?)", event, reason, publicReason, now.Add(evidenceRetention))
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), "INSERT INTO stream_termination_notices(stream_id,reason,expires_at) VALUES (?,?,?)", id, publicReason, now.Add(evidenceRetention))
	}
	if err != nil {
		problem(w, 503, "Could not record stream termination")
		return
	}
	if tx.Commit() != nil {
		problem(w, 503, "Could not end stream")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
