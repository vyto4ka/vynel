package backup

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"filippo.io/age"
)

// Backups may be encrypted with a password (setting backup.password): the archive is then an
// age file (https://age-encryption.org), so `age -d` opens it as well as `vynel restore`.

// EncryptedExt is added to the name of an encrypted backup.
const EncryptedExt = ".age"

var ageHeader = []byte("age-encryption.org/v1\n")

// ErrPasswordNeeded is returned for an encrypted backup opened without a password.
var ErrPasswordNeeded = errors.New("the backup is encrypted: give its password (--password or VYNEL_BACKUP_PASSWORD)")

// Encrypt returns the archive encrypted with password.
func Encrypt(archive []byte, password string) ([]byte, error) {
	r, err := age.NewScryptRecipient(password)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	w, err := age.Encrypt(&out, r)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(archive); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// IsEncrypted reports whether the file at path is an encrypted backup.
func IsEncrypted(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	head := make([]byte, len(ageHeader))
	n, _ := io.ReadFull(f, head)
	return bytes.Equal(head[:n], ageHeader), nil
}

// decryptTo writes the plain archive of an encrypted backup to a new file in dir and returns
// its path; the caller removes it.
func decryptTo(path, password, dir string) (string, error) {
	if password == "" {
		return "", ErrPasswordNeeded
	}
	id, err := age.NewScryptIdentity(password)
	if err != nil {
		return "", err
	}
	in, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer in.Close()
	r, err := age.Decrypt(bufio.NewReader(in), id)
	if err != nil {
		return "", fmt.Errorf("cannot decrypt the backup (wrong password?): %w", err)
	}
	out, err := os.CreateTemp(dir, ".restore-plain-")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		_ = os.Remove(out.Name())
		return "", fmt.Errorf("cannot decrypt the backup: %w", err)
	}
	return out.Name(), out.Close()
}

// InspectFile reads the meta of a backup file, encrypted (with password) or not.
func InspectFile(path, password string) (*Meta, error) {
	enc, err := IsEncrypted(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if !enc {
		return Inspect(f)
	}
	if password == "" {
		return nil, ErrPasswordNeeded
	}
	id, err := age.NewScryptIdentity(password)
	if err != nil {
		return nil, err
	}
	r, err := age.Decrypt(bufio.NewReader(f), id)
	if err != nil {
		return nil, fmt.Errorf("cannot decrypt the backup (wrong password?): %w", err)
	}
	return Inspect(r)
}
