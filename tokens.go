package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
)

func (a *App) tokensPage(w http.ResponseWriter, r *http.Request) {
	tokens, err := listTokens(a.db)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, "API Tokens", tokensHTML, map[string]any{"Tokens": tokens})
}

func (a *App) tokenCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	raw, token, err := createToken(a.db, r.FormValue("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	tokens, _ := listTokens(a.db)
	a.render(w, "API Tokens", tokensHTML, map[string]any{"Tokens": tokens, "NewToken": raw, "NewTokenName": token.Name})
}

func (a *App) tokenDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := a.db.Exec(`DELETE FROM api_tokens WHERE id = ?`, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/tokens", http.StatusSeeOther)
}

func (a *App) apiAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" {
			token = r.Header.Get("X-API-Token")
		}
		if token == "" || !validToken(a.db, token) {
			writeJSONError(w, http.StatusUnauthorized, "valid API token required")
			return
		}
		next(w, r)
	}
}

func createToken(db *sql.DB, name string) (string, Token, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", Token{}, errors.New("token name is required")
	}
	raw := "cfa_" + randomToken(32)
	hash := tokenHash(raw)
	prefix := raw
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	res, err := db.Exec(`INSERT INTO api_tokens (name, token_hash, prefix) VALUES (?, ?, ?)`, name, hash, prefix)
	if err != nil {
		return "", Token{}, err
	}
	id, _ := res.LastInsertId()
	return raw, Token{ID: id, Name: name, Prefix: prefix, CreatedAt: time.Now()}, nil
}

func listTokens(db *sql.DB) ([]Token, error) {
	rows, err := db.Query(`SELECT id, name, prefix, created_at, last_used_at FROM api_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tokens []Token
	for rows.Next() {
		var token Token
		var created string
		var last sql.NullString
		if err := rows.Scan(&token.ID, &token.Name, &token.Prefix, &created, &last); err != nil {
			return nil, err
		}
		token.CreatedAt = parseTime(created)
		if last.Valid {
			token.LastUsedAt = &last.String
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

func validToken(db *sql.DB, raw string) bool {
	hash := tokenHash(raw)
	res, err := db.Exec(`UPDATE api_tokens SET last_used_at = CURRENT_TIMESTAMP WHERE token_hash = ?`, hash)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

func tokenHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
