package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

func (p *passkeyServer) beginRecoveryCodeRotation(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if !decodeBody(w, r, &input) {
		return
	}
	p.backupTransaction(w, r, func(tx *sql.Tx, s backupSession) (int, any, error) {
		options, state, err := p.wa.BeginLogin(s.user, webauthn.WithUserVerification(protocol.VerificationRequired))
		if err != nil {
			return 0, nil, err
		}
		expires := minTime(p.verification.now().Add(p.challengeTTL), s.expires)
		options.Response.Timeout = int(expires.Sub(p.verification.now()).Milliseconds())
		err = p.saveBackupState(r.Context(), tx, s, "code_rotation", state, expires)
		return 200, options.Response, err
	})
}
func (p *passkeyServer) finishRecoveryCodeRotation(w http.ResponseWriter, r *http.Request) {
	p.backupFinish(w, r, "code_rotation", func(tx *sql.Tx, s backupSession, state webauthn.SessionData) (int, any, error) {
		credential, err := p.wa.FinishLogin(s.user, state, r)
		if err != nil {
			return backupError(401, "Passkey verification failed. Try again")
		}
		if credential.Authenticator.CloneWarning {
			return backupError(401, "Passkey verification failed. Try another Passkey")
		}
		raw, err := json.Marshal(credential)
		if err != nil {
			return 0, nil, err
		}
		hash := sha256.Sum256(credential.ID)
		if _, err = tx.ExecContext(r.Context(), `UPDATE passkey_credentials SET credential=? WHERE id_hash=? AND account_id=?`, raw, hash[:], s.user.ID); err != nil {
			return 0, nil, err
		}
		codes, err := replaceRecoveryCodes(r.Context(), tx, s.user.ID)
		return 200, map[string]any{"recoveryCodes": codes}, err
	})
}
