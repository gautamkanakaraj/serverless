package db

import (
	"testing"
)

func TestEncryptDecryptConnectionString(t *testing.T) {
	originalConnStr := "postgres://user:secret-password@neon-host.neon.tech/neondb?sslmode=require"

	encrypted, err := EncryptConnectionString(originalConnStr)
	if err != nil {
		t.Fatalf("Failed to encrypt connection string: %v", err)
	}

	if encrypted == originalConnStr {
		t.Errorf("Encrypted output is same as plain text")
	}

	decrypted, err := DecryptConnectionString(encrypted)
	if err != nil {
		t.Fatalf("Failed to decrypt connection string: %v", err)
	}

	if decrypted != originalConnStr {
		t.Errorf("Decrypted string does not match original: got %q, want %q", decrypted, originalConnStr)
	}
}
