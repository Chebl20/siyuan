package multiuser

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/mail"
	"regexp"
	"strings"

	"golang.org/x/crypto/argon2"
)

var slugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{2,62}$`)

const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32
)

func validateEmail(email string) error {
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != strings.TrimSpace(email) || len(email) > 254 {
		return errors.New("enter a valid email address")
	}
	return nil
}

func validatePassword(password string) error {
	if len(password) < 12 || len(password) > 256 {
		return errors.New("password must contain 12 to 256 characters")
	}
	return nil
}

func validateWorkspaceName(name string) error {
	if length := len([]rune(strings.TrimSpace(name))); length < 1 || length > 100 {
		return errors.New("workspace name must contain 1 to 100 characters")
	}
	return nil
}

func normalizeSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(value, "-")
	return strings.Trim(value, "-")
}

func validateSlug(slug string) error {
	if !slugPattern.MatchString(slug) {
		return errors.New("workspace address must use 3 to 63 lowercase letters, numbers or hyphens")
	}
	return nil
}

func randomToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return "$argon2id$v=19$m=65536,t=3,p=4$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(hash), nil
}

func comparePassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=65536,t=3,p=4" {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) != argonKeyLen {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	if len(actual) != len(expected) {
		return false
	}
	var difference byte
	for index := range actual {
		difference |= actual[index] ^ expected[index]
	}
	return difference == 0
}
