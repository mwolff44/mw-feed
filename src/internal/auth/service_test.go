// SPDX-License-Identifier: GPL-3.0-or-later

package auth_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mwolff44/mw-feed/internal/auth"
	"github.com/mwolff44/mw-feed/internal/store"
)

// fakeClock is a controllable clock.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

const (
	testLogin    = "alice"
	testPassword = "a sufficiently long passphrase"
)

func newService(t *testing.T) (*auth.Service, *fakeClock) {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clock := &fakeClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	return auth.NewService(st, clock), clock
}

func TestInstallCreatesTheOnlyUserOnce(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	installed, err := svc.Installed(ctx)
	if err != nil || installed {
		t.Fatalf("Installed before install = %v, %v; want false, nil", installed, err)
	}

	if err := svc.Install(ctx, testLogin, testPassword); err != nil {
		t.Fatalf("Install: %v", err)
	}

	installed, err = svc.Installed(ctx)
	if err != nil || !installed {
		t.Errorf("Installed after install = %v, %v; want true, nil", installed, err)
	}
	if err := svc.Install(ctx, "bob", "another sufficiently long passphrase"); !errors.Is(err, auth.ErrAlreadyInstalled) {
		t.Errorf("second Install error = %v, want auth.ErrAlreadyInstalled", err)
	}
}

func TestInstallEnforcesLoginAndPasswordRules(t *testing.T) {
	cases := []struct {
		name     string
		login    string
		password string
		want     error // nil means accepted
	}{
		{"login empty", "", testPassword, auth.ErrInvalidLogin},
		{"login blank", "   ", testPassword, auth.ErrInvalidLogin},
		{"password 14 characters", testLogin, strings.Repeat("a", 14), auth.ErrPasswordTooShort},
		{"password 15 characters", testLogin, strings.Repeat("a", 15), nil},
		{"password 128 characters", testLogin, strings.Repeat("a", 128), nil},
		{"password 129 characters", testLogin, strings.Repeat("a", 129), auth.ErrPasswordTooLong},
		{"14 emoji are 14 characters, not 56 bytes", testLogin, strings.Repeat("🔑", 14), auth.ErrPasswordTooShort},
		{"15 emoji are accepted", testLogin, strings.Repeat("🔑", 15), nil},
		{"spaces only are accepted when long enough", testLogin, strings.Repeat(" ", 15), nil},
		{"no composition rules", testLogin, "alllowercaseletters", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newService(t)

			err := svc.Install(context.Background(), tc.login, tc.password)

			if tc.want == nil && err != nil {
				t.Errorf("Install: %v, want success", err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("Install error = %v, want %v", err, tc.want)
			}
		})
	}
}

func installed(t *testing.T) (*auth.Service, *fakeClock) {
	t.Helper()
	svc, clock := newService(t)
	if err := svc.Install(context.Background(), testLogin, testPassword); err != nil {
		t.Fatalf("Install: %v", err)
	}
	return svc, clock
}

func login(t *testing.T, svc *auth.Service) auth.Session {
	t.Helper()
	sess, err := svc.Login(context.Background(), auth.LoginRequest{IP: "192.0.2.1", Login: testLogin, Password: testPassword})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	return sess
}

func TestLoginCreatesAnAuthenticatedSession(t *testing.T) {
	ctx := context.Background()
	svc, _ := installed(t)

	sess := login(t, svc)

	if len(sess.ID) < 32 || sess.CSRFToken == "" {
		t.Errorf("session = %+v, want a long random ID and a CSRF token", sess)
	}
	got, err := svc.Authenticate(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if !got.Authenticated || got.CSRFToken != sess.CSRFToken {
		t.Errorf("Authenticate = %+v, want authenticated session with the same CSRF token", got)
	}
}

func TestLoginRejectsBadCredentialsWithTheSameError(t *testing.T) {
	cases := []struct {
		name            string
		login, password string
	}{
		{"wrong password", testLogin, "not the right passphrase"},
		{"unknown login", "mallory", testPassword},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := installed(t)

			sess, err := svc.Login(context.Background(), auth.LoginRequest{IP: "192.0.2.1", Login: tc.login, Password: tc.password})

			if !errors.Is(err, auth.ErrInvalidCredentials) {
				t.Errorf("Login error = %v, want auth.ErrInvalidCredentials", err)
			}
			if sess.ID != "" {
				t.Errorf("a session was returned on failure: %+v", sess)
			}
		})
	}
}

func TestSessionExpiresAfterOneHourOfInactivity(t *testing.T) {
	ctx := context.Background()
	svc, clock := installed(t)
	sess := login(t, svc)

	clock.Advance(3599 * time.Second)
	if _, err := svc.Authenticate(ctx, sess.ID); err != nil {
		t.Fatalf("1s before expiry: %v", err)
	}
	// Activity renewed the session: 3599 s later it is still valid.
	clock.Advance(3599 * time.Second)
	if _, err := svc.Authenticate(ctx, sess.ID); err != nil {
		t.Fatalf("session was not renewed by activity: %v", err)
	}
	clock.Advance(3601 * time.Second)
	if _, err := svc.Authenticate(ctx, sess.ID); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("after 3601 s idle: error = %v, want auth.ErrNoSession", err)
	}
}

func TestAuthenticateRejectsUnknownSession(t *testing.T) {
	svc, _ := installed(t)

	if _, err := svc.Authenticate(context.Background(), "does-not-exist"); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("error = %v, want auth.ErrNoSession", err)
	}
}

func TestRememberedSessionLastsThirtyDaysRenewedOnEachVisit(t *testing.T) {
	ctx := context.Background()
	svc, clock := installed(t)
	sess, err := svc.Login(ctx, auth.LoginRequest{IP: "192.0.2.1", Login: testLogin, Password: testPassword, Remember: true})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !sess.Persistent {
		t.Fatal("session is not persistent")
	}
	day := 24 * time.Hour

	clock.Advance(2 * time.Hour) // would have expired without "remember me"
	if _, err := svc.Authenticate(ctx, sess.ID); err != nil {
		t.Fatalf("after 2 h: %v", err)
	}
	clock.Advance(29 * day)
	if _, err := svc.Authenticate(ctx, sess.ID); err != nil {
		t.Fatalf("29 days after last visit: %v", err)
	}
	clock.Advance(29 * day) // 58 days after login: only valid if renewed
	if _, err := svc.Authenticate(ctx, sess.ID); err != nil {
		t.Fatalf("session was not renewed by the previous visit: %v", err)
	}
	clock.Advance(31 * day)
	if _, err := svc.Authenticate(ctx, sess.ID); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("31 days idle: error = %v, want auth.ErrNoSession", err)
	}
}

func failLogin(svc *auth.Service, ip string) error {
	_, err := svc.Login(context.Background(), auth.LoginRequest{IP: ip, Login: testLogin, Password: "not the right passphrase"})
	return err
}

func goodLogin(svc *auth.Service, ip string) error {
	_, err := svc.Login(context.Background(), auth.LoginRequest{IP: ip, Login: testLogin, Password: testPassword})
	return err
}

func TestFourFailuresBanTheIPEvenForTheRightPassword(t *testing.T) {
	svc, clock := installed(t)
	const attacker = "192.0.2.66"

	for i := 1; i <= 4; i++ {
		if err := failLogin(svc, attacker); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("failure %d: error = %v, want auth.ErrInvalidCredentials", i, err)
		}
	}

	err := goodLogin(svc, attacker)
	var banned *auth.BannedError
	if !errors.Is(err, auth.ErrBanned) || !errors.As(err, &banned) {
		t.Fatalf("right password while banned: error = %v, want a *auth.BannedError", err)
	}
	if want := clock.Now().Add(1800 * time.Second); !banned.Until.Equal(want) {
		t.Errorf("ban until %v, want %v", banned.Until, want)
	}
	if err := goodLogin(svc, "192.0.2.7"); err != nil {
		t.Errorf("another IP is affected by the ban: %v", err)
	}
}

func TestBanEndsAfterThirtyMinutesWithoutBeingExtendedAndCounterRestarts(t *testing.T) {
	svc, clock := installed(t)
	const ip = "192.0.2.66"
	for range 4 {
		_ = failLogin(svc, ip)
	}

	clock.Advance(1000 * time.Second)
	if err := failLogin(svc, ip); !errors.Is(err, auth.ErrBanned) {
		t.Fatalf("attempt during ban: error = %v, want auth.ErrBanned", err)
	}
	clock.Advance(801 * time.Second) // 1801 s after the ban started
	// Three failures after the ban must not ban again: the counter restarted at 0.
	for i := 1; i <= 3; i++ {
		if err := failLogin(svc, ip); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("failure %d after the ban: error = %v, want auth.ErrInvalidCredentials", i, err)
		}
	}
	if err := goodLogin(svc, ip); err != nil {
		t.Errorf("login after 3 failures following the ban: %v", err)
	}
}

func TestSuccessfulLoginResetsTheFailureCounter(t *testing.T) {
	svc, _ := installed(t)
	const ip = "192.0.2.66"
	for range 3 {
		_ = failLogin(svc, ip)
	}
	if err := goodLogin(svc, ip); err != nil {
		t.Fatalf("login: %v", err)
	}

	for range 3 {
		_ = failLogin(svc, ip)
	}

	if err := goodLogin(svc, ip); err != nil {
		t.Errorf("failures were not consecutive but the IP got banned: %v", err)
	}
}

func TestLogoutEndsTheSession(t *testing.T) {
	ctx := context.Background()
	svc, _ := installed(t)
	sess := login(t, svc)

	if err := svc.Logout(ctx, sess.ID); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	if _, err := svc.Authenticate(ctx, sess.ID); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("Authenticate after logout: error = %v, want auth.ErrNoSession", err)
	}
	if err := svc.Logout(ctx, "does-not-exist"); err != nil {
		t.Errorf("Logout of an unknown session: %v, want nil", err)
	}
}

func TestAnonymousSessionCarriesACSRFTokenButIsNotAuthenticated(t *testing.T) {
	ctx := context.Background()
	svc, _ := installed(t)

	anon, err := svc.NewAnonymousSession(ctx)
	if err != nil {
		t.Fatalf("NewAnonymousSession: %v", err)
	}

	if anon.Authenticated || anon.ID == "" || anon.CSRFToken == "" {
		t.Errorf("anonymous session = %+v, want unauthenticated with an ID and a CSRF token", anon)
	}
	got, err := svc.Authenticate(ctx, anon.ID)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if got.Authenticated {
		t.Error("an anonymous session must not be authenticated")
	}
}

func TestLoginRegeneratesTheSessionIdentifier(t *testing.T) {
	ctx := context.Background()
	svc, _ := installed(t)
	anon, err := svc.NewAnonymousSession(ctx)
	if err != nil {
		t.Fatalf("NewAnonymousSession: %v", err)
	}

	sess, err := svc.Login(ctx, auth.LoginRequest{
		IP: "192.0.2.1", Login: testLogin, Password: testPassword, AnonymousSessionID: anon.ID,
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	if sess.ID == anon.ID || sess.CSRFToken == anon.CSRFToken {
		t.Error("session ID and CSRF token must change at login")
	}
	if _, err := svc.Authenticate(ctx, anon.ID); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("the pre-login session is still valid: error = %v", err)
	}
}

func TestVerifyCSRFAcceptsOnlyTheTokenOfThatSession(t *testing.T) {
	svc, _ := installed(t)
	one, two := login(t, svc), login(t, svc)

	cases := []struct {
		name  string
		token string
		want  bool
	}{
		{"own token", one.CSRFToken, true},
		{"token of another session", two.CSRFToken, false},
		{"empty token", "", false},
		{"altered token", one.CSRFToken + "x", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := auth.VerifyCSRF(one, tc.token); got != tc.want {
				t.Errorf("VerifyCSRF = %v, want %v", got, tc.want)
			}
		})
	}
}

const newPassword = "an even better passphrase 42"

func TestChangePasswordKeepsOnlyTheCurrentSessionWithANewIdentifier(t *testing.T) {
	ctx := context.Background()
	svc, _ := installed(t)
	current, other := login(t, svc), login(t, svc)

	renewed, err := svc.ChangePassword(ctx, current.ID, testPassword, newPassword)
	if err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	if renewed.ID == current.ID || renewed.CSRFToken == current.CSRFToken || !renewed.Authenticated {
		t.Errorf("renewed session = %+v, want new ID and CSRF token, authenticated", renewed)
	}
	if _, err := svc.Authenticate(ctx, renewed.ID); err != nil {
		t.Errorf("renewed session is not usable: %v", err)
	}
	for name, id := range map[string]string{"old current ID": current.ID, "other session": other.ID} {
		if _, err := svc.Authenticate(ctx, id); !errors.Is(err, auth.ErrNoSession) {
			t.Errorf("%s still valid after password change: error = %v", name, err)
		}
	}
	if err := goodLogin(svc, "192.0.2.9"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("old password still works: error = %v", err)
	}
	_, err = svc.Login(ctx, auth.LoginRequest{IP: "192.0.2.9", Login: testLogin, Password: newPassword})
	if err != nil {
		t.Errorf("new password does not work: %v", err)
	}
}

func TestChangePasswordRefusesBadRequestsAndChangesNothing(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name            string
		current, next   string
		anonymousCaller bool
		want            error
	}{
		{"wrong current password", "not the right passphrase", newPassword, false, auth.ErrInvalidCredentials},
		{"new password too short", testPassword, "short", false, auth.ErrPasswordTooShort},
		{"new password too long", testPassword, strings.Repeat("a", 129), false, auth.ErrPasswordTooLong},
		{"caller not authenticated", testPassword, newPassword, true, auth.ErrNoSession},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := installed(t)
			caller := login(t, svc)
			id := caller.ID
			if tc.anonymousCaller {
				anon, err := svc.NewAnonymousSession(ctx)
				if err != nil {
					t.Fatalf("NewAnonymousSession: %v", err)
				}
				id = anon.ID
			}

			_, err := svc.ChangePassword(ctx, id, tc.current, tc.next)

			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if _, err := svc.Authenticate(ctx, caller.ID); err != nil {
				t.Errorf("the caller's session was ended by a refused change: %v", err)
			}
			if err := goodLogin(svc, "192.0.2.9"); err != nil {
				t.Errorf("the password changed despite the refusal: %v", err)
			}
		})
	}
}

// countRows counts rows of a table straight in the database file. Housekeeping
// has no public observable: this is a deliberate exception to "test through
// the interface".
func countRows(t *testing.T, dataDir, table string) int {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "mw-feed.db"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestCreatingASessionPurgesExpiredSessionsAndEndedBans(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	st, err := store.Open(ctx, dataDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clock := &fakeClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	svc := auth.NewService(st, clock)
	if err := svc.Install(ctx, testLogin, testPassword); err != nil {
		t.Fatalf("Install: %v", err)
	}
	// One anonymous, one plain and one remembered session, plus a banned IP.
	if _, err := svc.NewAnonymousSession(ctx); err != nil {
		t.Fatal(err)
	}
	if err := goodLogin(svc, "192.0.2.1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(ctx, auth.LoginRequest{IP: "192.0.2.1", Login: testLogin, Password: testPassword, Remember: true}); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		_ = failLogin(svc, "192.0.2.66")
	}
	if got := countRows(t, dataDir, "sessions"); got != 3 {
		t.Fatalf("sessions before = %d, want 3", got)
	}

	clock.Advance(3601 * time.Second) // anonymous and plain sessions expired, ban over
	if _, err := svc.NewAnonymousSession(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, dataDir, "sessions"); got != 2 { // remembered + the new one
		t.Errorf("sessions after 1 h = %d, want 2", got)
	}
	if got := countRows(t, dataDir, "login_failures"); got != 0 {
		t.Errorf("login_failures after the ban ended = %d, want 0", got)
	}

	clock.Advance(31 * 24 * time.Hour)
	if _, err := svc.NewAnonymousSession(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, dataDir, "sessions"); got != 1 { // only the newest
		t.Errorf("sessions after 31 days = %d, want 1", got)
	}
}
