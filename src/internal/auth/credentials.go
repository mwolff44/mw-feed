// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// Password length bounds follow OWASP guidance for single-factor passwords:
// at least 15 characters, and a generous maximum to protect the hashing step.
const (
	minPasswordChars = 15
	maxPasswordChars = 128
)

var (
	// ErrInvalidLogin means the login is empty once trimmed.
	ErrInvalidLogin = errors.New("login must not be empty")
	// ErrPasswordTooShort means the password has fewer than 15 characters.
	ErrPasswordTooShort = errors.New("password must have at least 15 characters")
	// ErrPasswordTooLong means the password has more than 128 characters.
	ErrPasswordTooLong = errors.New("password must have at most 128 characters")
)

// normalizeLogin trims the login and rejects an empty one.
func normalizeLogin(login string) (string, error) {
	login = strings.TrimSpace(login)
	if login == "" {
		return "", ErrInvalidLogin
	}
	return login, nil
}

// checkPassword enforces the length rules, counted in Unicode characters.
func checkPassword(password string) error {
	switch n := utf8.RuneCountInString(password); {
	case n < minPasswordChars:
		return ErrPasswordTooShort
	case n > maxPasswordChars:
		return ErrPasswordTooLong
	}
	return nil
}
