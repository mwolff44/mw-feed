// SPDX-License-Identifier: GPL-3.0-or-later

package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mwolff44/mw-feed/internal/auth"
)

const goodPassword = "correct horse battery staple"

func TestVerifyPasswordAcceptsOnlyTheHashedPassword(t *testing.T) {
	hash, err := auth.HashPassword(goodPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	ok, err := auth.VerifyPassword(hash, goodPassword)
	if err != nil || !ok {
		t.Errorf("VerifyPassword(right) = %v, %v; want true, nil", ok, err)
	}

	ok, err = auth.VerifyPassword(hash, "wrong password entirely")
	if err != nil || ok {
		t.Errorf("VerifyPassword(wrong) = %v, %v; want false, nil", ok, err)
	}
}

func TestHashPasswordUsesArgon2idWithARandomSalt(t *testing.T) {
	first, err := auth.HashPassword(goodPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	second, err := auth.HashPassword(goodPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	if !strings.HasPrefix(first, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("hash = %q, want an argon2id PHC string with the fixed parameters", first)
	}
	if first == second {
		t.Error("two hashes of the same password are identical: salt is not random")
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	for _, hash := range []string{"", "plaintext", "$argon2id$v=19$m=19456,t=2,p=1$onlysalt", "$bcrypt$x$y$z$w"} {
		ok, err := auth.VerifyPassword(hash, goodPassword)
		if ok || !errors.Is(err, auth.ErrMalformedHash) {
			t.Errorf("VerifyPassword(%q) = %v, %v; want false, ErrMalformedHash", hash, ok, err)
		}
	}
}
