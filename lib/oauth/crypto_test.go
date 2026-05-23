package oauth

import (
	"bytes"
	"testing"
)

func TestSealerRoundtrip(t *testing.T) {
	key := bytes.Repeat([]byte{0x01}, 32)
	s, err := NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte(`{"refresh_token":"abc"}`)
	ct, err := s.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(ct, plain) {
		t.Fatal("ciphertext equals plaintext")
	}
	got, err := s.Open(ct)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("roundtrip mismatch: %q vs %q", got, plain)
	}
}

func TestNewSealerBadKey(t *testing.T) {
	if _, err := NewSealer(make([]byte, 8)); err == nil {
		t.Fatal("expected error for short key")
	}
}

func TestSealerOpenShort(t *testing.T) {
	s, _ := NewSealer(make([]byte, 32))
	if _, err := s.Open([]byte{0x00}); err == nil {
		t.Fatal("expected error for short ciphertext")
	}
}

func TestSealerOpenTamper(t *testing.T) {
	s, _ := NewSealer(bytes.Repeat([]byte{0x01}, 32))
	ct, _ := s.Seal([]byte("hi"))
	ct[len(ct)-1] ^= 0xff
	if _, err := s.Open(ct); err == nil {
		t.Fatal("expected GCM auth failure")
	}
}
