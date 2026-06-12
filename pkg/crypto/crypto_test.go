package crypto

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"testing"

	"golang.org/x/crypto/curve25519"
)

func TestGenerateIdentity(t *testing.T) {
	pub1, priv1, err := GenerateIdentity()
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}
	if len(pub1) != ed25519.PublicKeySize || len(priv1) != ed25519.PrivateKeySize {
		t.Fatalf("bad key sizes: pub=%d priv=%d", len(pub1), len(priv1))
	}
	pub2, _, err := GenerateIdentity()
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}
	if bytes.Equal(pub1, pub2) {
		t.Fatal("two generated identities are identical")
	}
}

func TestSignVerify(t *testing.T) {
	pub, priv, _ := GenerateIdentity()
	digest := Digest([]byte("hello gandr"))

	sig, err := Sign(priv, digest[:])
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if len(sig) != SignatureSize {
		t.Fatalf("signature size = %d, want %d", len(sig), SignatureSize)
	}

	tests := []struct {
		name   string
		pub    []byte
		digest []byte
		sig    []byte
		want   bool
	}{
		{"valid", pub, digest[:], sig, true},
		{"wrong digest", pub, []byte("other"), sig, false},
		{"truncated sig", pub, digest[:], sig[:63], false},
		{"empty sig", pub, digest[:], nil, false},
		{"corrupt sig", pub, digest[:], append(append([]byte{}, sig[:63]...), sig[63]^0xFF), false},
		{"wrong pubkey", func() []byte { p, _, _ := GenerateIdentity(); return p }(), digest[:], sig, false},
		{"short pubkey", pub[:31], digest[:], sig, false},
		{"nil pubkey", nil, digest[:], sig, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Verify(tt.pub, tt.digest, tt.sig); got != tt.want {
				t.Errorf("Verify = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSignRejectsBadKey(t *testing.T) {
	if _, err := Sign(make([]byte, 12), []byte("digest")); err == nil {
		t.Fatal("Sign accepted a malformed private key")
	}
}

func TestDigest(t *testing.T) {
	// Digest over multiple parts must equal SHA-256 of the concatenation.
	want := sha256.Sum256([]byte("abcdef"))
	got := Digest([]byte("ab"), []byte("cd"), []byte("ef"))
	if got != want {
		t.Fatal("multi-part digest mismatch with concatenated SHA-256")
	}
	empty := Digest()
	wantEmpty := sha256.Sum256(nil)
	if empty != wantEmpty {
		t.Fatal("empty digest mismatch")
	}
}

func TestKeyConversionECDHAgreement(t *testing.T) {
	// The whole point of the conversion: ECDH between converted keys of
	// two identities must agree from both sides.
	pubA, privA, _ := GenerateIdentity()
	pubB, privB, _ := GenerateIdentity()

	xPubA, err := PublicKeyToX25519(pubA)
	if err != nil {
		t.Fatalf("PublicKeyToX25519(A): %v", err)
	}
	xPubB, err := PublicKeyToX25519(pubB)
	if err != nil {
		t.Fatalf("PublicKeyToX25519(B): %v", err)
	}
	xPrivA, err := PrivateKeyToX25519(privA)
	if err != nil {
		t.Fatalf("PrivateKeyToX25519(A): %v", err)
	}
	xPrivB, err := PrivateKeyToX25519(privB)
	if err != nil {
		t.Fatalf("PrivateKeyToX25519(B): %v", err)
	}

	// converted private key must regenerate the converted public key
	derived, err := curve25519.X25519(xPrivA[:], curve25519.Basepoint)
	if err != nil {
		t.Fatalf("X25519 basepoint: %v", err)
	}
	if !bytes.Equal(derived, xPubA[:]) {
		t.Fatal("converted private key does not match converted public key")
	}

	s1, err := DeriveSharedKey(xPrivA, xPubB, "test")
	if err != nil {
		t.Fatalf("DeriveSharedKey(A->B): %v", err)
	}
	s2, err := DeriveSharedKey(xPrivB, xPubA, "test")
	if err != nil {
		t.Fatalf("DeriveSharedKey(B->A): %v", err)
	}
	if s1 != s2 {
		t.Fatal("ECDH shared keys do not agree")
	}

	s3, err := DeriveSharedKey(xPrivA, xPubB, "other-context")
	if err != nil {
		t.Fatalf("DeriveSharedKey other info: %v", err)
	}
	if s1 == s3 {
		t.Fatal("different HKDF info produced identical keys")
	}
}

func TestKeyConversionErrors(t *testing.T) {
	if _, err := PublicKeyToX25519(make([]byte, 5)); err == nil {
		t.Error("PublicKeyToX25519 accepted short key")
	}
	// y=2 is not the y-coordinate of any point on the Edwards curve
	bad := make([]byte, 32)
	bad[0] = 2
	if _, err := PublicKeyToX25519(bad); err == nil {
		t.Error("PublicKeyToX25519 accepted invalid point")
	}
	if _, err := PrivateKeyToX25519(make([]byte, 5)); err == nil {
		t.Error("PrivateKeyToX25519 accepted short key")
	}
}

func TestPrivateKeyToX25519Clamped(t *testing.T) {
	_, priv, _ := GenerateIdentity()
	x, err := PrivateKeyToX25519(priv)
	if err != nil {
		t.Fatal(err)
	}
	if x[0]&7 != 0 || x[31]&128 != 0 || x[31]&64 == 0 {
		t.Fatal("X25519 private key is not clamped per RFC 7748")
	}
}

func TestGenerateX25519(t *testing.T) {
	pub, priv, err := GenerateX25519()
	if err != nil {
		t.Fatalf("GenerateX25519: %v", err)
	}
	derived, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(derived, pub[:]) {
		t.Fatal("X25519 public key does not match private key")
	}
	pub2, _, _ := GenerateX25519()
	if pub == pub2 {
		t.Fatal("two generated X25519 keys are identical")
	}
}

func TestRandomBytes(t *testing.T) {
	a := RandomBytes(32)
	if len(a) != 32 {
		t.Fatalf("len = %d, want 32", len(a))
	}
	if bytes.Equal(a, RandomBytes(32)) {
		t.Fatal("two random reads are identical")
	}
	if z := RandomBytes(0); len(z) != 0 {
		t.Fatal("RandomBytes(0) should return empty slice")
	}
}
