package main

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strings"
)

type broadcastPermission struct {
	BroadcastingAllowed bool    `json:"broadcastingAllowed"`
	Reason              *string `json:"reason"`
}

func (s *broadcasterServer) broadcastRestrictionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /staff/streams/{id}/end", s.p.verification.protect(s.forceEndStream))
	mux.HandleFunc("GET /account/broadcast-permission", s.ownBroadcastPermission)
	mux.HandleFunc("GET /staff/accounts/{account}/broadcast-permission", s.staffBroadcastPermission)
	mux.HandleFunc("POST /staff/accounts/{account}/broadcast-permission", s.p.verification.protect(s.changeBroadcastPermission))
}
func (s *broadcasterServer) ownBroadcastPermission(w http.ResponseWriter, r *http.Request) {
	s.getBroadcastPermission(w, r, false)
}
func (s *broadcasterServer) staffBroadcastPermission(w http.ResponseWriter, r *http.Request) {
	s.getBroadcastPermission(w, r, true)
}
func (s *broadcasterServer) getBroadcastPermission(w http.ResponseWriter, r *http.Request, staff bool) {
	w.Header().Set("Cache-Control", "no-store")
	account, ok := s.authenticate(w, r, staff)
	if !ok {
		return
	}
	if staff {
		var err error
		account, err = hex.DecodeString(r.PathValue("account"))
		if err != nil || len(account) != 32 {
			problem(w, 400, "Invalid account ID")
			return
		}
	}
	var allowed sql.NullBool
	var reason sql.NullString
	err := s.p.verification.db.QueryRowContext(r.Context(), `SELECT p.broadcasting_allowed,p.suspension_reason FROM accounts a LEFT JOIN account_broadcast_permissions p ON p.account_id=a.id WHERE a.id=?`, account).Scan(&allowed, &reason)
	if err == sql.ErrNoRows {
		problem(w, 404, "Account not found")
		return
	}
	if err != nil {
		problem(w, 503, "Could not load broadcasting permission")
		return
	}
	result := broadcastPermission{BroadcastingAllowed: !allowed.Valid || allowed.Bool}
	if !result.BroadcastingAllowed && reason.Valid {
		result.Reason = &reason.String
	}
	writeJSON(w, 200, result)
}
func (s *broadcasterServer) changeBroadcastPermission(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, ok := s.authenticate(w, r, true)
	if !ok {
		return
	}
	account, err := hex.DecodeString(r.PathValue("account"))
	if err != nil || len(account) != 32 {
		problem(w, 400, "Invalid account ID")
		return
	}
	var input struct {
		BroadcastingAllowed *bool  `json:"broadcastingAllowed"`
		Reason              string `json:"reason"`
		PublicReason        string `json:"publicReason"`
		ReportID            string `json:"reportId"`
	}
	if !decodeBodyLimit(w, r, &input, 16384) {
		return
	}
	reason := strings.TrimSpace(input.Reason)
	publicReason := strings.TrimSpace(input.PublicReason)
	if input.BroadcastingAllowed == nil || !validRestrictionText(reason, 2000) || (!*input.BroadcastingAllowed && !validRestrictionText(publicReason, 500)) || (*input.BroadcastingAllowed && publicReason != "") {
		problem(w, 400, "Provide a staff reason and, for suspension, a separate reason visible to the account")
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
		problem(w, 503, "Could not change broadcasting permission")
		return
	}
	defer tx.Rollback()
	// Publishing takes this same lock. Once this change commits, existing sessions
	// and retries must observe it before accepting another comment.
	var locked []byte
	err = tx.QueryRowContext(r.Context(), "SELECT id FROM accounts WHERE id=? FOR UPDATE", account).Scan(&locked)
	if err == sql.ErrNoRows {
		problem(w, 404, "Account not found")
		return
	}
	if err != nil {
		problem(w, 503, "Could not change broadcasting permission")
		return
	}
	now := s.p.verification.now()
	if report != nil {
		var target []byte
		err = tx.QueryRowContext(r.Context(), "SELECT target_id FROM user_reports WHERE id=? AND expires_at>? FOR UPDATE", report, now).Scan(&target)
		if err == sql.ErrNoRows || (err == nil && !bytes.Equal(target, account)) {
			problem(w, 400, "Report does not match this account or has expired")
			return
		}
		if err != nil {
			problem(w, 503, "Could not check report")
			return
		}
	}
	allowed := true
	err = tx.QueryRowContext(r.Context(), "SELECT broadcasting_allowed FROM account_broadcast_permissions WHERE account_id=? FOR UPDATE", account).Scan(&allowed)
	if err != nil && err != sql.ErrNoRows {
		problem(w, 503, "Could not check broadcasting permission")
		return
	}
	if allowed == *input.BroadcastingAllowed {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !*input.BroadcastingAllowed {
		if err = stopAccountPublishing(r.Context(), tx, account, now); err != nil {
			problem(w, 503, "Could not stop broadcasting")
			return
		}
	}
	var visible any
	if !*input.BroadcastingAllowed {
		visible = publicReason
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO account_broadcast_permissions(account_id,broadcasting_allowed,suspension_reason) VALUES (?,?,?) ON DUPLICATE KEY UPDATE broadcasting_allowed=VALUES(broadcasting_allowed),suspension_reason=VALUES(suspension_reason)`, account, *input.BroadcastingAllowed, visible)
	kind := "broadcasting_suspended"
	if *input.BroadcastingAllowed {
		kind = "broadcasting_restored"
	}
	var event int64
	if err == nil {
		event, err = recordActivity(r.Context(), tx, kind, actor, account, nil, nil, report, now)
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), "INSERT INTO broadcast_permission_actions(event_id,reason,public_reason,expires_at) VALUES (?,?,?,?)", event, reason, visible, now.Add(evidenceRetention))
	}
	if err != nil {
		problem(w, 503, "Could not record broadcasting permission change")
		return
	}
	if tx.Commit() != nil {
		problem(w, 503, "Could not change broadcasting permission")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
