package usertransfer

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"filippo.io/age"
)

// An export may be encrypted with a password: the file carries uuid and subscription tokens,
// so it opens every user's subscription. Encrypted files are age files, like backups —
// `age -d` reads them too.

// ageHeader is the first line of an age file (how a file is recognized without opening it).
var ageHeader = []byte("age-encryption.org/v1\n")

// ErrPasswordNeeded is returned for an encrypted export opened without a password.
var ErrPasswordNeeded = errors.New("the export is encrypted: give its password (--password)")

// IsEncrypted reports whether b starts as an age file.
func IsEncrypted(b []byte) bool { return bytes.HasPrefix(b, ageHeader) }

// Encrypt wraps a plain export file with a password.
func Encrypt(plain []byte, password string) ([]byte, error) {
	r, err := age.NewScryptRecipient(password)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	w, err := age.Encrypt(&out, r)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(plain); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Decrypt unwraps an encrypted export (plain files come back as they are).
func Decrypt(b []byte, password string) ([]byte, error) {
	if !IsEncrypted(b) {
		return b, nil
	}
	if password == "" {
		return nil, ErrPasswordNeeded
	}
	id, err := age.NewScryptIdentity(password)
	if err != nil {
		return nil, err
	}
	r, err := age.Decrypt(bytes.NewReader(b), id)
	if err != nil {
		return nil, fmt.Errorf("cannot decrypt the export (wrong password?): %w", err)
	}
	return io.ReadAll(io.LimitReader(r, 256<<20))
}
