package main

import (
	"database/sql"
	"encoding/hex"
	"net/http"
)

type streamParticipant struct {
	AccountID       string  `json:"accountId"`
	Namespace       *string `json:"namespace"`
	PublishingState string  `json:"publishingState"`
}

func (s *broadcasterServer) publisherRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /streams/{id}/participants", s.participants)
	mux.HandleFunc("POST /streams/{id}/publishing", s.p.verification.protect(s.publishing))
	mux.HandleFunc("POST /relay/authorize", s.authorizeRelay)
}

func (s *broadcasterServer) participants(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	id, err := streamID(r.PathValue("id"))
	if err != nil {
		problem(w, 400, "Invalid stream ID")
		return
	}
	var permitted int
	err = s.p.verification.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM live_streams s WHERE s.id=?
 AND (s.creator_id=? OR EXISTS (SELECT 1 FROM stream_participants p WHERE p.stream_id=s.id AND p.account_id=? AND p.left_at IS NULL))`, id, actor, actor).Scan(&permitted)
	if err != nil {
		problem(w, 503, "Could not check participation")
		return
	}
	if permitted == 0 {
		problem(w, 403, "Participation required")
		return
	}
	cutoff := s.p.verification.now().Add(-publisherLease)
	rows, err := s.p.verification.db.QueryContext(r.Context(), `SELECT p.account_id,`+visiblePublishing+`
 FROM stream_participants p JOIN live_streams s ON s.id=p.stream_id
 WHERE p.stream_id=? AND p.left_at IS NULL AND s.state='live' ORDER BY p.joined_at,p.account_id`, cutoff, cutoff, id)
	if err != nil {
		problem(w, 503, "Could not load participants")
		return
	}
	defer rows.Close()
	result := []streamParticipant{}
	for rows.Next() {
		var raw []byte
		var namespace sql.NullString
		var itemState string
		if err = rows.Scan(&raw, &namespace, &itemState); err != nil {
			problem(w, 503, "Could not load participants")
			return
		}
		item := streamParticipant{AccountID: hex.EncodeToString(raw), PublishingState: itemState}
		if namespace.Valid {
			item.Namespace = &namespace.String
		}
		result = append(result, item)
	}
	if rows.Err() != nil {
		problem(w, 503, "Could not load participants")
		return
	}
	writeJSON(w, 200, result)
}

func (s *broadcasterServer) publishing(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	id, err := streamID(r.PathValue("id"))
	if err != nil {
		problem(w, 400, "Invalid stream ID")
		return
	}
	var input struct {
		SessionID    string `json:"sessionId"`
		Stop         bool   `json:"stop"`
		Prepare      bool   `json:"prepare"`
		Disconnected bool   `json:"disconnected"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	session, err := streamID(input.SessionID)
	if err != nil {
		problem(w, 400, "Invalid publication session ID")
		return
	}
	namespace := "mitemau/streams/" + hex.EncodeToString(id) + "/publishers/" + hex.EncodeToString(actor) + "/" + hex.EncodeToString(session)
	tx, err := s.p.verification.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Could not update publishing")
		return
	}
	defer tx.Rollback()
	if err = lockBroadcastAccounts(r.Context(), tx, actor); err != nil {
		problem(w, 503, "Could not check publishing permission")
		return
	}
	var state string
	err = tx.QueryRowContext(r.Context(), "SELECT state FROM live_streams WHERE id=? FOR UPDATE", id).Scan(&state)
	if err == sql.ErrNoRows {
		problem(w, 404, "Stream not found")
		return
	}
	if err != nil {
		problem(w, 503, "Could not update publishing")
		return
	}
	credential := s.publisherCredential(namespace)
	if input.Stop {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO stream_publishing_grants(stream_id,account_id,session_id,token_hash,revoked_at)
 VALUES (?,?,?,?,?) ON DUPLICATE KEY UPDATE revoked_at=COALESCE(revoked_at,VALUES(revoked_at))`, id, actor, session, tokenHash(credential), s.p.verification.now())
		if err != nil {
			problem(w, 503, "Could not revoke publishing")
			return
		}
		publishingState := "stopped"
		if input.Disconnected {
			publishingState = "disconnected"
		}
		_, err = tx.ExecContext(r.Context(), "UPDATE stream_participants SET publisher_namespace=NULL,publisher_seen_at=NULL,publishing_state=? WHERE stream_id=? AND account_id=? AND publisher_namespace=?", publishingState, id, actor, namespace)
	} else {
		if state != "live" {
			problem(w, 409, "Stream is not live")
			return
		}
		var allowed int
		err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM stream_participants p JOIN broadcaster_applications b ON b.account_id=p.account_id
 WHERE p.stream_id=? AND p.account_id=? AND p.left_at IS NULL AND b.approved_at IS NOT NULL AND NOT EXISTS (SELECT 1 FROM account_broadcast_permissions bp WHERE bp.account_id=p.account_id AND bp.broadcasting_allowed=FALSE)`, id, actor).Scan(&allowed)
		if err != nil {
			problem(w, 503, "Could not check publishing permission")
			return
		}
		if allowed == 0 {
			problem(w, 403, "Active participation and broadcasting permission required")
			return
		}
		if input.Prepare {
			_, err = tx.ExecContext(r.Context(), `INSERT INTO stream_publishing_grants(stream_id,account_id,session_id,token_hash)
 VALUES (?,?,?,?) ON DUPLICATE KEY UPDATE token_hash=token_hash`, id, actor, session, tokenHash(credential))
			if err != nil {
				problem(w, 503, "Could not prepare publishing")
				return
			}
		}
		var permitted int
		err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM stream_publishing_grants WHERE stream_id=? AND account_id=? AND session_id=? AND revoked_at IS NULL AND token_hash=?", id, actor, session, tokenHash(credential)).Scan(&permitted)
		if err != nil {
			problem(w, 503, "Could not check publishing grant")
			return
		}
		if permitted == 0 {
			problem(w, 409, "Prepare a new publishing session")
			return
		}
		if !input.Prepare {
			var seen sql.NullTime
			err = tx.QueryRowContext(r.Context(), "SELECT last_relay_seen_at FROM stream_publishing_grants WHERE stream_id=? AND account_id=? AND session_id=?", id, actor, session).Scan(&seen)
			if err != nil {
				problem(w, 503, "Could not check Relay connection")
				return
			}
			if !seen.Valid || !seen.Time.After(s.p.verification.now().Add(-publisherLease)) {
				problem(w, 409, "Connect a new publishing session to Relay")
				return
			}
			_, err = tx.ExecContext(r.Context(), "UPDATE stream_publishing_grants SET revoked_at=? WHERE stream_id=? AND account_id=? AND session_id<>? AND revoked_at IS NULL", s.p.verification.now(), id, actor, session)
			if err == nil {
				_, err = tx.ExecContext(r.Context(), "UPDATE stream_participants SET publisher_namespace=?,publisher_seen_at=?,publishing_state='publishing' WHERE stream_id=? AND account_id=?", namespace, seen.Time, id, actor)
			}
		}
	}
	if err != nil {
		problem(w, 503, "Could not update publishing")
		return
	}
	if err = tx.Commit(); err != nil {
		problem(w, 503, "Could not update publishing")
		return
	}
	result := map[string]string{"namespace": namespace}
	if input.Prepare {
		result["token"] = credential
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, result)
}
