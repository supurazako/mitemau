package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
)

// Codes have 128 bits of entropy. Store only domain-separated hashes; plaintext
// is returned once to the browser and never stored in cookies or logs.
func recoveryCodeHash(code string) ([]byte, error) {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	raw, err := hex.DecodeString(normalized)
	if err != nil || len(raw) != 16 {
		return nil, errors.New("invalid recovery code")
	}
	hash := sha256.Sum256(append([]byte("mitemau-recovery-code\x00"), raw...))
	return hash[:], nil
}
func replaceRecoveryCodes(ctx context.Context, tx *sql.Tx, accountID []byte) ([]string, error) {
	if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE account_id=?`, accountID); err != nil {
		return nil, err
	}
	codes := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			return nil, err
		}
		value := hex.EncodeToString(raw)
		groups := make([]string, 0, 8)
		for j := 0; j < len(value); j += 4 {
			groups = append(groups, value[j:j+4])
		}
		code := strings.Join(groups, "-")
		hash, _ := recoveryCodeHash(code)
		if _, err := tx.ExecContext(ctx, `INSERT INTO recovery_codes (code_hash,account_id) VALUES (?,?)`, hash, accountID); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, nil
}
