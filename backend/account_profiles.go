package main

import (
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
)

var displayIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,23}$`)

type accountProfile struct {
	DisplayID   string `json:"displayId"`
	DisplayName string `json:"displayName"`
}

func (s *broadcasterServer) profileRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /account/profile", s.ownProfile)
	mux.HandleFunc("POST /account/profile", s.p.verification.protect(s.saveProfile))
}

func (s *broadcasterServer) ownProfile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	account, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	var profile accountProfile
	err := s.p.verification.db.QueryRowContext(r.Context(), "SELECT display_id,display_name FROM account_profiles WHERE account_id=?", account).Scan(&profile.DisplayID, &profile.DisplayName)
	if err != nil && err != sql.ErrNoRows {
		problem(w, 503, "Could not load profile")
		return
	}
	writeJSON(w, 200, profile)
}

func (s *broadcasterServer) saveProfile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	account, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	var profile accountProfile
	if !decodeBody(w, r, &profile) {
		return
	}
	profile.DisplayID = strings.ToLower(strings.TrimSpace(profile.DisplayID))
	profile.DisplayName = strings.TrimSpace(profile.DisplayName)
	if !displayIDPattern.MatchString(profile.DisplayID) {
		problem(w, 400, "Use 3–24 letters, numbers, underscores or hyphens for the display ID. Start with a letter or number.")
		return
	}
	if profile.DisplayName == "" || !utf8.ValidString(profile.DisplayName) || utf8.RuneCountInString(profile.DisplayName) > 80 || strings.IndexFunc(profile.DisplayName, unicode.IsControl) >= 0 {
		problem(w, 400, "Enter a display name of 1–80 characters on one line")
		return
	}
	// Serialize setup and updates for the same account. The unique index handles
	// competing accounts without an upsert ever updating another account's row.
	tx, err := s.p.verification.db.BeginTx(r.Context(), nil)
	if err == nil {
		defer tx.Rollback()
		var locked []byte
		err = tx.QueryRowContext(r.Context(), "SELECT id FROM accounts WHERE id=? FOR UPDATE", account).Scan(&locked)
		if err == nil {
			var existing string
			err = tx.QueryRowContext(r.Context(), "SELECT display_id FROM account_profiles WHERE account_id=?", account).Scan(&existing)
			switch err {
			case nil:
				_, err = tx.ExecContext(r.Context(), "UPDATE account_profiles SET display_id=?,display_name=? WHERE account_id=?", profile.DisplayID, profile.DisplayName, account)
			case sql.ErrNoRows:
				_, err = tx.ExecContext(r.Context(), "INSERT INTO account_profiles(account_id,display_id,display_name) VALUES (?,?,?)", account, profile.DisplayID, profile.DisplayName)
			}
		}
		if err == nil {
			err = tx.Commit()
		}
	}
	var duplicate *mysql.MySQLError
	if errors.As(err, &duplicate) && duplicate.Number == 1062 {
		problem(w, 409, "That display ID is already in use")
		return
	}
	if err != nil {
		problem(w, 503, "Could not save profile")
		return
	}
	writeJSON(w, 200, profile)
}
