package usertransfer

import (
	"bytes"
	"errors"
	"testing"
)

// An encrypted export round-trips; a wrong or missing password does not open it.
func TestExportEncryption(t *testing.T) {
	plain := []byte(`{"meta":{"format":1,"source":"vynel","users":1},"users":[{"username":"a","uuid":"u"}]}`)

	enc, err := Encrypt(plain, "sekret")
	if err != nil {
		t.Fatal(err)
	}
	if !IsEncrypted(enc) || bytes.Equal(enc, plain) {
		t.Fatalf("not encrypted: %d bytes", len(enc))
	}
	if IsEncrypted(plain) {
		t.Error("plain file taken for encrypted")
	}

	back, err := Decrypt(enc, "sekret")
	if err != nil || !bytes.Equal(back, plain) {
		t.Fatalf("decrypt: %v %d bytes", err, len(back))
	}
	if _, err := Decrypt(enc, ""); !errors.Is(err, ErrPasswordNeeded) {
		t.Fatalf("no password: %v", err)
	}
	if _, err := Decrypt(enc, "wrong"); err == nil {
		t.Fatal("wrong password accepted")
	}
	if b, err := Decrypt(plain, ""); err != nil || !bytes.Equal(b, plain) {
		t.Fatalf("plain through Decrypt: %v", err)
	}
}
