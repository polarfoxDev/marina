package crypto

import (
	"bytes"
	"crypto/rand"
	"io"
	"testing"
)

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	// A few MB so we cross age's internal STREAM chunk boundary (64KiB).
	plaintext := make([]byte, 3*1024*1024+17)
	if _, err := rand.Read(plaintext); err != nil {
		t.Fatalf("generate plaintext: %v", err)
	}

	var buf bytes.Buffer
	wc, err := Encrypt(&buf, "correct horse battery staple")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := wc.Write(plaintext); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := wc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	r, err := Decrypt(&buf, "correct horse battery staple")
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round-trip mismatch: got %d bytes, want %d bytes", len(got), len(plaintext))
	}
}

func TestEncryptDecrypt_EmptyInput(t *testing.T) {
	var buf bytes.Buffer
	wc, err := Encrypt(&buf, "pw")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if err := wc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	r, err := Decrypt(&buf, "pw")
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty plaintext, got %d bytes", len(got))
	}
}

func TestDecrypt_WrongPassword(t *testing.T) {
	var buf bytes.Buffer
	wc, err := Encrypt(&buf, "right-password")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := wc.Write([]byte("secret data")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := wc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Wrong password may fail immediately at Decrypt() (header/MAC check)
	// or on the first Read(), depending on age's internals -- assert on
	// whichever stage actually errors.
	r, err := Decrypt(&buf, "wrong-password")
	if err == nil {
		_, err = io.ReadAll(r)
	}
	if err == nil {
		t.Fatal("expected error decrypting with wrong password, got nil")
	}
}

func TestDecrypt_TruncatedObject(t *testing.T) {
	var buf bytes.Buffer
	wc, err := Encrypt(&buf, "pw")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// Enough data to guarantee at least one full STREAM chunk plus a final
	// chunk, so truncating drops the authenticated terminal chunk.
	payload := make([]byte, 128*1024)
	if _, err := wc.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := wc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	full := buf.Bytes()
	truncated := bytes.NewReader(full[:len(full)-10])

	r, err := Decrypt(truncated, "pw")
	if err == nil {
		_, err = io.ReadAll(r)
	}
	if err == nil {
		t.Fatal("expected error reading truncated ciphertext, got nil")
	}
}

func TestEncryptDecrypt_NoPasswordIsPassthrough(t *testing.T) {
	plaintext := []byte("stored as-is, no encryption")

	var buf bytes.Buffer
	wc, err := Encrypt(&buf, "")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := wc.Write(plaintext); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := wc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if !bytes.Equal(buf.Bytes(), plaintext) {
		t.Fatalf("expected byte-exact passthrough on write, got %q", buf.Bytes())
	}

	r, err := Decrypt(&buf, "")
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("expected byte-exact passthrough on read, got %q", got)
	}
}

func TestEncrypt_UnclosedWriterProducesUndecryptableObject(t *testing.T) {
	var buf bytes.Buffer
	wc, err := Encrypt(&buf, "pw")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := wc.Write([]byte("some data that never gets finalized")); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Deliberately NOT calling wc.Close().

	r, err := Decrypt(bytes.NewReader(buf.Bytes()), "pw")
	if err == nil {
		_, err = io.ReadAll(r)
	}
	if err == nil {
		t.Fatal("expected error decrypting an object whose writer was never closed, got nil")
	}
}
