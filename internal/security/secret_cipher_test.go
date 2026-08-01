package security

import (
	"encoding/base64"
	"strings"
	"testing"
)

func cipherForTest(t *testing.T, keyByte byte) *SecretCipher {
	t.Helper()
	cipher, err := NewSecretCipher(base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string(keyByte), 32))))
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	return cipher
}

func TestSecretCipherRoundTripAndRandomNonce(t *testing.T) {
	cipher := cipherForTest(t, 'a')
	first, err := cipher.Encrypt("top secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	second, err := cipher.Encrypt("top secret")
	if err != nil {
		t.Fatalf("encrypt second value: %v", err)
	}
	if first == second || !strings.HasPrefix(first, encryptedPrefix) {
		t.Fatal("expected versioned ciphertexts with independent nonces")
	}
	plaintext, err := cipher.Decrypt(first)
	if err != nil || plaintext != "top secret" {
		t.Fatalf("decrypt: plaintext=%q err=%v", plaintext, err)
	}
}

func TestSecretCipherRejectsPlaintextTamperingAndWrongKey(t *testing.T) {
	first := cipherForTest(t, 'a')
	second := cipherForTest(t, 'b')
	ciphertext, err := first.Encrypt("top secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	for name, testCase := range map[string]struct {
		value  string
		cipher *SecretCipher
	}{
		"plaintext": {"top secret", first},
		"tampered":  {ciphertext[:len(ciphertext)-1] + "A", first},
		"wrong key": {ciphertext, second},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := testCase.cipher.Decrypt(testCase.value); err == nil {
				t.Fatal("expected decryption to fail")
			}
		})
	}
}

func TestNewSecretCipherRequiresBase64Encoded32ByteKey(t *testing.T) {
	for _, key := range []string{"not base64!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := NewSecretCipher(key); err == nil {
			t.Fatalf("expected key %q to be rejected", key)
		}
	}
}
