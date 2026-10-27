package main

// auth.go - passwords, sessions, API tokens and the request guard.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	pbkdf2Iterations = 210000
	sessionCookie    = "foghorn_session"
	sessionLifetime  = 12 * time.Hour
	minPasswordLen   = 10
)

// pbkdf2SHA256 is RFC 8018 PBKDF2 with HMAC-SHA256. It is written out here so
// the server has no third-party dependencies beyond the Windows service glue.
func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	blocks := (keyLen + hashLen - 1) / hashLen
	out := make([]byte, 0, blocks*hashLen)
	var idx [4]byte
	for block := 1; block <= blocks; block++ {
		prf.Reset()
		prf.Write(salt)
		binary.BigEndian.PutUint32(idx[:], uint32(block))
		prf.Write(idx[:])
		u := prf.Sum(nil)
		t := make([]byte, len(u))
		copy(t, u)
		for i := 1; i < iter; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

func hashPassword(pw string) string {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	dk := pbkdf2SHA256([]byte(pw), salt, pbkdf2Iterations, 32)
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdf2Iterations,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(dk))
}

func verifyPassword(stored, pw string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1000 || iter > 10000000 {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[2])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got := pbkdf2SHA256([]byte(pw), salt, iter, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// A throwaway hash so a login for an unknown username costs the same time as a
// real one and usernames cannot be discovered by timing.
var dummyHash = hashPassword("foghorn-dummy-password")

func generatePassword() string {
	// No look-alike characters, so it can be read off a screen and typed.
	const alphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b[:4]) + "-" + string(b[4:8]) + "-" + string(b[8:12]) + "-" + string(b[12:])
}

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// ---- sessions ----

type session struct {
	userID  string
	expires time.Time
}

type sessionStore struct {
	mu sync.Mutex
	m  map[string]session
}

func newSessionStore() *sessionStore { return &sessionStore{m: map[string]session{}} }

func (ss *sessionStore) create(userID string) string {
	id := randomSecret(32)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	now := time.Now()
	for k, v := range ss.m {
		if now.After(v.expires) {
			delete(ss.m, k)
		}
	}
	ss.m[id] = session{userID: userID, expires: now.Add(sessionLifetime)}
	return id
}

func (ss *sessionStore) lookup(id string) (string, bool) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, ok := ss.m[id]
	if !ok || time.Now().After(s.expires) {
		delete(ss.m, id)
		return "", false
	}
	return s.userID, true
}

func (ss *sessionStore) drop(id string) {
	ss.mu.Lock()
	delete(ss.m, id)
	ss.mu.Unlock()
}

func (ss *sessionStore) dropUser(userID string) {
	ss.mu.Lock()
	for k, v := range ss.m {
		if v.userID == userID {
			delete(ss.m, k)
		}
	}
	ss.mu.Unlock()
}

// ---- login throttling ----

type throttle struct {
	mu sync.Mutex
	m  map[string]*attempts
}

type attempts struct {
	fails       int
	first       time.Time
	lockedUntil time.Time
}

func newThrottle() *throttle { return &throttle{m: map[string]*attempts{}} }

// blocked returns how long the caller must wait, or zero.
func (t *throttle) blocked(key string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	if a := t.m[key]; a != nil {
		if d := time.Until(a.lockedUntil); d > 0 {
			return d
		}
	}
	return 0
}

func (t *throttle) fail(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	if len(t.m) > 5000 {
		for k, a := range t.m {
			if now.Sub(a.first) > 30*time.Minute {
				delete(t.m, k)
			}
		}
	}
	a := t.m[key]
	if a == nil || now.Sub(a.first) > 10*time.Minute {
		a = &attempts{first: now}
		t.m[key] = a
	}
	a.fails++
	if a.fails >= 5 {
		a.lockedUntil = now.Add(5 * time.Minute)
		a.fails = 0
		a.first = now
	}
}

func (t *throttle) reset(key string) {
	t.mu.Lock()
	delete(t.m, key)
	t.mu.Unlock()
}

// ---- request guard ----

// identity is who is making an admin API call.
type identity struct {
	User     User // a copy, safe to read without the store lock
	ViaToken bool
	Scopes   []string // what a token may do; nil for people signed in
	session  string
}

// Token scopes. A token holds any combination.
var validScopes = map[string]string{
	"send": "Send, recall and schedule alerts",
	"lock": "Lock and unlock screens",
	"read": "Read computers, groups, sounds and history",
}

// tokenScopes treats tokens made before scopes existed as they always
// behaved: able to send and read, but not lock.
func tokenScopes(t *Token) []string {
	if t.Scopes == nil {
		return []string{"send", "read"}
	}
	return t.Scopes
}

func hasScope(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// can reports whether the caller may use a scope at all. People get "send"
// and "read" by being signed in; "lock" needs an administrator or an account
// an administrator has allowed to lock screens.
func (id *identity) can(scope string) bool {
	if id.ViaToken {
		return scope == "" || hasScope(id.Scopes, scope)
	}
	if scope == "lock" {
		return id.User.Role == "admin" || id.User.CanLock
	}
	return true
}

// authenticate resolves the caller from a Bearer token or session cookie.
func (a *App) authenticate(r *http.Request) (*identity, bool) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		sum := hashToken(strings.TrimSpace(h[7:]))
		a.store.mu.Lock()
		defer a.store.mu.Unlock()
		for _, t := range a.store.State.Tokens {
			if subtle.ConstantTimeCompare([]byte(t.Hash), []byte(sum)) == 1 {
				if time.Since(t.LastUsed) > time.Minute {
					t.LastUsed = time.Now()
					a.store.dirtyState = true
				}
				scopes := tokenScopes(t)
				return &identity{ViaToken: true, Scopes: append([]string(nil), scopes...), User: User{
					ID: "token:" + t.ID, Username: "token:" + t.Name,
					DisplayName: t.Name, Role: "sender", CanLock: hasScope(scopes, "lock"),
				}}, true
			}
		}
		return nil, false
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil, false
	}
	uid, ok := a.sessions.lookup(c.Value)
	if !ok {
		return nil, false
	}
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	u := a.store.userByID(uid)
	if u == nil || u.Disabled {
		return nil, false
	}
	return &identity{User: *u, session: c.Value}, true
}

type authedHandler func(w http.ResponseWriter, r *http.Request, id *identity)

// guard wraps a handler with authentication, role and scope checks, CSRF
// protection and the "you must change your password first" gate. scope is
// what an API token needs to make the call ("" = any valid token).
func (a *App) guard(role, scope string, h authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := a.authenticate(r)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "not_signed_in", "Sign in to continue.")
			return
		}
		// Browsers will not add a custom header to a cross-site form post, so
		// requiring one on every change blocks CSRF. Tokens are not sent
		// automatically by browsers, so they are exempt.
		if !id.ViaToken && r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("X-Foghorn") == "" {
				writeErr(w, http.StatusForbidden, "csrf", "Missing X-Foghorn header.")
				return
			}
		}
		if role == "admin" && id.User.Role != "admin" {
			writeErr(w, http.StatusForbidden, "forbidden", "Only administrators can do that.")
			return
		}
		if !id.can(scope) {
			if id.ViaToken {
				writeErr(w, http.StatusForbidden, "forbidden", "This API token does not have the \""+scope+"\" scope.")
			} else {
				writeErr(w, http.StatusForbidden, "forbidden", "Your account is not allowed to lock screens. Ask an administrator.")
			}
			return
		}
		if id.User.MustChange && role != "self" {
			writeErr(w, http.StatusForbidden, "password_change_required", "Choose a new password before continuing.")
			return
		}
		h(w, r, id)
	}
}

func (a *App) checkClientKey(r *http.Request) bool {
	got := r.Header.Get("X-Foghorn-Key")
	a.store.mu.Lock()
	want := a.store.State.Settings.ClientKey
	a.store.mu.Unlock()
	return want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
