package main

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

type broadcasterServer struct {
	p            *passkeyServer
	staffIDs     map[string]bool
	relayAuthKey []byte
}

func parseStaffIDs(value string) (map[string]bool, error) {
	ids := make(map[string]bool)
	if strings.TrimSpace(value) == "" {
		return ids, nil
	}
	for _, item := range strings.Split(value, ",") {
		raw, err := hex.DecodeString(strings.TrimSpace(item))
		if err != nil || len(raw) != 32 {
			return nil, fmt.Errorf("STAFF_ACCOUNT_IDS must contain comma-separated 64-character hexadecimal account IDs")
		}
		ids[hex.EncodeToString(raw)] = true
	}
	return ids, nil
}

func (s *broadcasterServer) routes(mux *http.ServeMux) {
	s.streamRoutes(mux)
	s.moderationRoutes(mux)
	s.commentRoutes(mux)
	mux.HandleFunc("GET /account/broadcaster", s.status)
	mux.HandleFunc("POST /account/broadcaster", s.p.verification.protect(s.apply))
	mux.HandleFunc("GET /staff/broadcasters", s.list)
	mux.HandleFunc("POST /staff/broadcasters/{id}/approve", s.p.verification.protect(s.approve))
}

func (s *broadcasterServer) authenticate(w http.ResponseWriter, r *http.Request, staffOnly bool) ([]byte, bool) {
	token := cookieToken(r, sessionCookie)
	if token == "" {
		problem(w, 401, "Sign in to continue")
		return nil, false
	}
	var id []byte
	now := s.p.verification.now()
	err := s.p.verification.db.QueryRowContext(r.Context(), "SELECT account_id FROM account_sessions WHERE token_hash=? AND expires_at>? AND absolute_expires_at>?", tokenHash(token), now, now).Scan(&id)
	if err == sql.ErrNoRows {
		problem(w, 401, "Sign in to continue")
		return nil, false
	}
	if err != nil {
		problem(w, 503, "Account is temporarily unavailable")
		return nil, false
	}
	if staffOnly && !s.staffIDs[hex.EncodeToString(id)] {
		problem(w, 403, "Staff access required")
		return nil, false
	}
	return id, true
}

func (s *broadcasterServer) status(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	var approved sql.NullTime
	err := s.p.verification.db.QueryRowContext(r.Context(), "SELECT approved_at FROM broadcaster_applications WHERE account_id=?", id).Scan(&approved)
	status := "not_applied"
	if err == nil {
		status = "pending"
		if approved.Valid {
			status = "approved"
		}
	} else if err != sql.ErrNoRows {
		problem(w, 503, "Application is temporarily unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"status": status, "staff": s.staffIDs[hex.EncodeToString(id)], "accountId": hex.EncodeToString(id)})
}

func (s *broadcasterServer) apply(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	var input struct{}
	if !decodeBody(w, r, &input) {
		return
	}
	_, err := s.p.verification.db.ExecContext(r.Context(), "INSERT INTO broadcaster_applications (account_id,requested_at) VALUES (?,?) ON DUPLICATE KEY UPDATE account_id=VALUES(account_id)", id, s.p.verification.now())
	if err != nil {
		problem(w, 503, "Could not submit application")
		return
	}
	s.status(w, r)
}

func (s *broadcasterServer) list(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticate(w, r, true); !ok {
		return
	}
	rows, err := s.p.verification.db.QueryContext(r.Context(), "SELECT a.account_id,u.email FROM broadcaster_applications a JOIN accounts u ON u.id=a.account_id WHERE a.approved_at IS NULL ORDER BY a.requested_at,a.account_id")
	if err != nil {
		problem(w, 503, "Applications are temporarily unavailable")
		return
	}
	defer rows.Close()
	applications := []map[string]string{}
	for rows.Next() {
		var id []byte
		var email string
		if err = rows.Scan(&id, &email); err != nil {
			problem(w, 503, "Could not read applications")
			return
		}
		applications = append(applications, map[string]string{"accountId": hex.EncodeToString(id), "email": email})
	}
	if rows.Err() != nil {
		problem(w, 503, "Could not read applications")
		return
	}
	writeJSON(w, 200, applications)
}

func (s *broadcasterServer) approve(w http.ResponseWriter, r *http.Request) {
	staff, ok := s.authenticate(w, r, true)
	if !ok {
		return
	}
	id, err := hex.DecodeString(r.PathValue("id"))
	if err != nil || len(id) != 32 {
		problem(w, 400, "Invalid account ID")
		return
	}
	var input struct{}
	if !decodeBody(w, r, &input) {
		return
	}
	tx, err := s.p.verification.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Could not approve application")
		return
	}
	defer tx.Rollback()
	now := s.p.verification.now()
	result, err := tx.ExecContext(r.Context(), "UPDATE broadcaster_applications SET approved_at=?,approved_by=? WHERE account_id=? AND approved_at IS NULL", now, staff, id)
	if err != nil {
		problem(w, 503, "Could not approve application")
		return
	}
	count, err := result.RowsAffected()
	if err != nil {
		problem(w, 503, "Could not approve application")
		return
	}
	if count == 0 {
		problem(w, 409, "Application is not pending")
		return
	}
	if _, err = recordActivity(r.Context(), tx, "broadcaster_approved", staff, id, nil, nil, nil, now); err != nil {
		problem(w, 503, "Could not record approval")
		return
	}
	if err = tx.Commit(); err != nil {
		problem(w, 503, "Could not approve application")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "approved"})
}
