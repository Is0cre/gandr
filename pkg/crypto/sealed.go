package crypto

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"
)

// SealedBlockSize is the padding block size for sealed messages. The
// inner plaintext is padded to a multiple of this size so that a node
// relaying the blob learns only a coarse size bucket, never the true
// message length.
const SealedBlockSize = 1024

// sealedKeyInfo is the HKDF domain separator for sealed message keys.
const sealedKeyInfo = "gandr-sealed-v1"

// sealedInnerContext domain-separates the inner signature digest from
// every other signature in the protocol.
const sealedInnerContext = "gandr-sealed-inner-v1"

// Inner plaintext layout (before zero-padding to SealedBlockSize):
//
//	[sender_pubkey:    32 bytes]  always present
//	[inner_signature:  64 bytes]  present only if not deniable
//	[message_type:      1 byte ]
//	[content_len:       4 bytes]  uint32 big-endian
//	[content:        variable  ]
//	[padding:        variable  ]  zeros to SealedBlockSize boundary
//
// The content_len field makes padding removal unambiguous; trailing
// zeros in user content can never be confused with padding.
const (
	sealedHeaderMin = ed25519.PublicKeySize + 1 + 4                 // deniable
	sealedHeaderMax = sealedHeaderMin + ed25519.SignatureSize        // signed
)

// MaxSealedContent is the largest content that fits a sealed message
// while keeping the outer envelope payload under the protocol's 64 KiB
// payload limit.
const MaxSealedContent = 63 * SealedBlockSize // 64512 bytes of blocks, minus headers checked at seal time

// SealedBox is the encrypted, node-opaque form of a sealed message. A
// node sees nothing in it except the ephemeral public key, the nonce,
// the deniability flag, and a padded ciphertext.
type SealedBox struct {
	EphemeralPubkey [KeySize]byte
	Nonce           [NonceSize]byte
	Deniable        bool
	Ciphertext      []byte
}

// SealedContent is the decrypted result of opening a SealedBox.
type SealedContent struct {
	SenderPubkey [KeySize]byte // claimed sender; cryptographically proven only if !Deniable
	MessageType  uint8
	Content      []byte
	Deniable     bool
}

// ErrSealedCorrupt is returned when a sealed message decrypts but its
// inner structure is malformed.
var ErrSealedCorrupt = errors.New("crypto: sealed message structure corrupt")

// ErrSealedSignature is returned when a non-deniable sealed message
// carries an invalid inner signature.
var ErrSealedSignature = errors.New("crypto: sealed message inner signature invalid")

// sealedInnerDigest computes the digest the inner signature covers. It
// binds the content to the recipient so a sealed message cannot be
// re-sealed to a third party while keeping the original signature.
func sealedInnerDigest(recipientPub ed25519.PublicKey, msgType uint8, content []byte) [32]byte {
	return Digest([]byte(sealedInnerContext), recipientPub, []byte{msgType}, content)
}

// Seal encrypts content to the holder of recipientPub's Ed25519
// identity key using the Noise X pattern: a fresh ephemeral X25519 key
// performs ECDH against the recipient's converted identity key, and the
// HKDF-derived key encrypts the padded inner plaintext with
// XChaCha20-Poly1305.
//
// If deniable is true the inner plaintext carries no signature: the
// recipient can read the message but cannot cryptographically prove who
// wrote it.
func Seal(senderPriv ed25519.PrivateKey, recipientPub ed25519.PublicKey, msgType uint8, content []byte, deniable bool) (*SealedBox, error) {
	if len(senderPriv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("crypto: invalid sender private key length %d", len(senderPriv))
	}
	if len(content) > MaxSealedContent {
		return nil, fmt.Errorf("crypto: sealed content too large: %d > %d", len(content), MaxSealedContent)
	}

	recipientX, err := PublicKeyToX25519(recipientPub)
	if err != nil {
		return nil, fmt.Errorf("crypto: converting recipient key: %w", err)
	}
	ephPub, ephPriv, err := GenerateX25519()
	if err != nil {
		return nil, err
	}
	key, err := DeriveSharedKey(ephPriv, recipientX, sealedKeyInfo)
	if err != nil {
		return nil, err
	}

	headerLen := sealedHeaderMin
	if !deniable {
		headerLen = sealedHeaderMax
	}
	padded := paddedLen(headerLen + len(content))
	plain := make([]byte, padded)

	senderPub := senderPriv.Public().(ed25519.PublicKey)
	off := copy(plain, senderPub)
	if !deniable {
		digest := sealedInnerDigest(recipientPub, msgType, content)
		// senderPriv length was validated above; ed25519.Sign cannot fail
		off += copy(plain[off:], ed25519.Sign(senderPriv, digest[:]))
	}
	plain[off] = msgType
	off++
	binary.BigEndian.PutUint32(plain[off:], uint32(len(content)))
	off += 4
	copy(plain[off:], content)
	// remainder of plain is already zero padding

	nonce, ct, err := Encrypt(key, plain, nil)
	if err != nil {
		return nil, err
	}
	box := &SealedBox{Nonce: nonce, Deniable: deniable, Ciphertext: ct}
	copy(box.EphemeralPubkey[:], ephPub[:])
	return box, nil
}

// Open decrypts a SealedBox addressed to the holder of recipientPriv.
// For non-deniable messages it verifies the inner signature against the
// claimed sender public key and fails if it does not match.
func Open(recipientPriv ed25519.PrivateKey, box *SealedBox) (*SealedContent, error) {
	if box == nil {
		return nil, errors.New("crypto: nil sealed box")
	}
	recipientX, err := PrivateKeyToX25519(recipientPriv)
	if err != nil {
		return nil, err
	}
	key, err := DeriveSharedKey(recipientX, box.EphemeralPubkey, sealedKeyInfo)
	if err != nil {
		return nil, err
	}
	plain, err := Decrypt(key, box.Nonce, box.Ciphertext, nil)
	if err != nil {
		return nil, err
	}
	if len(plain)%SealedBlockSize != 0 {
		return nil, ErrSealedCorrupt
	}

	headerLen := sealedHeaderMin
	if !box.Deniable {
		headerLen = sealedHeaderMax
	}
	if len(plain) < headerLen {
		return nil, ErrSealedCorrupt
	}

	out := &SealedContent{Deniable: box.Deniable}
	off := copy(out.SenderPubkey[:], plain[:KeySize])
	var innerSig []byte
	if !box.Deniable {
		innerSig = plain[off : off+ed25519.SignatureSize]
		off += ed25519.SignatureSize
	}
	out.MessageType = plain[off]
	off++
	contentLen := binary.BigEndian.Uint32(plain[off:])
	off += 4
	if int(contentLen) > len(plain)-off {
		return nil, ErrSealedCorrupt
	}
	out.Content = plain[off : off+int(contentLen)]
	// verify padding is all zeros; non-zero padding indicates tampering
	// upstream of encryption or a non-conforming sender
	for _, b := range plain[off+int(contentLen):] {
		if b != 0 {
			return nil, ErrSealedCorrupt
		}
	}

	if !box.Deniable {
		recipientPub := recipientPriv.Public().(ed25519.PublicKey)
		digest := sealedInnerDigest(recipientPub, out.MessageType, out.Content)
		if !Verify(out.SenderPubkey[:], digest[:], innerSig) {
			return nil, ErrSealedSignature
		}
	}
	return out, nil
}

// paddedLen returns n rounded up to the next multiple of
// SealedBlockSize. A length already on a boundary is unchanged, except
// zero-length input still occupies one block.
func paddedLen(n int) int {
	if n == 0 {
		return SealedBlockSize
	}
	return ((n + SealedBlockSize - 1) / SealedBlockSize) * SealedBlockSize
}
