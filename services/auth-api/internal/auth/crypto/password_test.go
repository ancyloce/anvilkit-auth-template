package crypto

import (
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("Passw0rd!", bcrypt.MinCost)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if err := VerifyPassword(hash, "Passw0rd!"); err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}
}

func TestVerifyPasswordWrongPassword(t *testing.T) {
	hash, err := HashPassword("Passw0rd!", bcrypt.MinCost)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if err := VerifyPassword(hash, "wrong-password"); !errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		t.Fatalf("VerifyPassword() error = %v, want %v", err, bcrypt.ErrMismatchedHashAndPassword)
	}
}
