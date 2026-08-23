// Package crypto provides optional password-based encryption for backup
// archives, built on filippo.io/age. All of the tricky parts of a streaming
// encryption container -- KDF, chunking, authentication, truncation
// detection -- are provided by age's audited implementation; this package
// only adds a pass-through for the "no password configured" case, so
// callers never need an if/else between encrypted and plaintext paths.
package crypto

import (
	"fmt"
	"io"

	"filippo.io/age"
)

// nopWriteCloser adapts an io.Writer that doesn't need closing (or whose
// Close is handled elsewhere) to io.WriteCloser, for the no-password
// pass-through case.
type nopWriteCloser struct {
	io.Writer
}

func (nopWriteCloser) Close() error { return nil }

// Encrypt wraps w so that everything written to the returned WriteCloser is
// age-encrypted (scrypt/password-based) before reaching w. If password is
// empty, the returned WriteCloser writes straight through to w unencrypted.
//
// The caller MUST Close() the returned WriteCloser (and check the error) --
// that is what flushes age's final authenticated chunk. Failing to close it
// silently produces a truncated, undecryptable object.
func Encrypt(w io.Writer, password string) (io.WriteCloser, error) {
	if password == "" {
		return nopWriteCloser{w}, nil
	}

	recipient, err := age.NewScryptRecipient(password)
	if err != nil {
		return nil, fmt.Errorf("create age recipient: %w", err)
	}

	wc, err := age.Encrypt(w, recipient)
	if err != nil {
		return nil, fmt.Errorf("start age encryption: %w", err)
	}

	return wc, nil
}

// Decrypt wraps r so that reads from the returned Reader yield the
// age-decrypted plaintext. If password is empty, reads pass straight
// through from r unchanged.
//
// A wrong password, a truncated object, or a tampered object all surface as
// an error either from Decrypt itself or from a subsequent Read -- age's
// STREAM construction authenticates every chunk and refuses to return
// unauthenticated bytes.
func Decrypt(r io.Reader, password string) (io.Reader, error) {
	if password == "" {
		return r, nil
	}

	identity, err := age.NewScryptIdentity(password)
	if err != nil {
		return nil, fmt.Errorf("create age identity: %w", err)
	}

	rd, err := age.Decrypt(r, identity)
	if err != nil {
		return nil, fmt.Errorf("start age decryption: %w", err)
	}

	return rd, nil
}
