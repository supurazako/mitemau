package main

import (
	"bytes"
	"context"
	"database/sql"
	"sort"
	"time"
)

const approvedBroadcastSQL = `SELECT COUNT(*) FROM broadcaster_applications b LEFT JOIN account_broadcast_permissions bp ON bp.account_id=b.account_id WHERE b.account_id=? AND b.approved_at IS NOT NULL AND COALESCE(bp.broadcasting_allowed,TRUE)=TRUE`

// All permission-changing and permission-consuming paths lock accounts before
// streams. Invitations lock both accounts in a stable order.
func lockBroadcastAccounts(ctx context.Context, tx *sql.Tx, accounts ...[]byte) error {
	sort.Slice(accounts, func(i, j int) bool { return bytes.Compare(accounts[i], accounts[j]) < 0 })
	for i, account := range accounts {
		if i > 0 && bytes.Equal(account, accounts[i-1]) {
			continue
		}
		var id []byte
		if err := tx.QueryRowContext(ctx, "SELECT id FROM accounts WHERE id=? FOR UPDATE", account).Scan(&id); err != nil {
			return err
		}
	}
	return nil
}
func stopAccountPublishing(ctx context.Context, tx *sql.Tx, account []byte, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `SELECT stream_id FROM stream_participants WHERE account_id=? UNION SELECT stream_id FROM stream_publishing_grants WHERE account_id=? ORDER BY stream_id`, account, account)
	if err != nil {
		return err
	}
	var streams [][]byte
	for rows.Next() {
		var id []byte
		if err = rows.Scan(&id); err != nil {
			break
		}
		streams = append(streams, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, stream := range streams {
		var id []byte
		if err = tx.QueryRowContext(ctx, "SELECT id FROM live_streams WHERE id=? FOR UPDATE", stream).Scan(&id); err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE stream_publishing_grants SET revoked_at=COALESCE(revoked_at,?) WHERE stream_id=? AND account_id=?", now, stream, account); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE stream_participants SET publisher_namespace=NULL,publisher_seen_at=NULL,publishing_state='stopped' WHERE stream_id=? AND account_id=?", stream, account); err != nil {
			return err
		}
	}
	return nil
}
