package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func (a *App) loginPage(w http.ResponseWriter, r *http.Request) {
	a.render(w, "Login", loginHTML, map[string]any{"Configured": a.adminConfigured(), "Error": r.URL.Query().Get("error"), "LoggedOut": true})
}

func (a *App) loginPost(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	_ = purgeOldAttempts(a.db)
	banned, _ := isBanned(a.db, ip)
	if banned {
		http.Error(w, "too many invalid attempts; try again later", http.StatusTooManyRequests)
		return
	}
	if !a.adminConfigured() {
		http.Redirect(w, r, "/login?error="+urlText("admin credentials are not configured"), http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if a.validAdmin(r.FormValue("username"), r.FormValue("password")) {
		_ = clearAttempt(a.db, ip)
		a.setSession(w, r.FormValue("username"))
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	_ = recordFailedAttempt(a.db, ip)
	http.Redirect(w, r, "/login?error="+urlText("invalid username or password"), http.StatusSeeOther)
}

func (a *App) logoutPost(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *App) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.validSession(r) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (a *App) adminConfigured() bool {
	return a.adminUsername != "" && (a.adminPassword != "" || a.adminHash != "")
}

func (a *App) validAdmin(username, password string) bool {
	if username != a.adminUsername {
		return false
	}
	if a.adminPassword != "" {
		return subtleEqual(a.adminPassword, password)
	}
	return bcrypt.CompareHashAndPassword([]byte(a.adminHash), []byte(password)) == nil
}

func (a *App) setSession(w http.ResponseWriter, username string) {
	expires := time.Now().Add(sessionTTL).Unix()
	payload := fmt.Sprintf("%s|%d", username, expires)
	sig := sign(a.sessionSecret, payload)
	value := base64.RawURLEncoding.EncodeToString([]byte(payload + "|" + sig))
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: value, Path: "/", Expires: time.Unix(expires, 0), HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func (a *App) validSession(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return false
	}
	parts := strings.Split(string(decoded), "|")
	if len(parts) != 3 {
		return false
	}
	payload := parts[0] + "|" + parts[1]
	if !subtleEqual(sign(a.sessionSecret, payload), parts[2]) {
		return false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	return parts[0] == a.adminUsername
}

func purgeOldAttempts(db *sql.DB) error {
	_, err := db.Exec(`DELETE FROM login_attempts WHERE last_attempt_at < ?`, time.Now().Add(-banWindow).UTC().Format(time.RFC3339))
	return err
}

func isBanned(db *sql.DB, ip string) (bool, error) {
	var banned int
	err := db.QueryRow(`SELECT banned FROM login_attempts WHERE ip = ?`, ip).Scan(&banned)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return banned == 1, err
}

func recordFailedAttempt(db *sql.DB, ip string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(`INSERT INTO login_attempts (ip, attempts, banned, last_attempt_at) VALUES (?, 1, 0, ?)
		ON CONFLICT(ip) DO UPDATE SET attempts = attempts + 1, banned = CASE WHEN attempts + 1 >= ? THEN 1 ELSE banned END, last_attempt_at = ?`,
		ip, now, maxLoginFails, now)
	return err
}

func clearAttempt(db *sql.DB, ip string) error {
	_, err := db.Exec(`DELETE FROM login_attempts WHERE ip = ?`, ip)
	return err
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func sign(secret []byte, payload string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func subtleEqual(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}

func clientIP(r *http.Request) string {
	for _, header := range []string{"X-Forwarded-For", "X-Real-IP"} {
		value := r.Header.Get(header)
		if value != "" {
			return strings.TrimSpace(strings.Split(value, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
