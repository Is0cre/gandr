# Sealed Messages

Written from the implementation in `pkg/crypto/sealed.go`.

A sealed message is opaque to every node that carries it. A node sees:
the recipient pubkey on the envelope, the ephemeral key, the nonce, the
deniability flag, a padded ciphertext, and timing. It does not see the
sender's identity (the outer envelope may be signed by a throwaway
key), the true message length, or any content. A fully compromised node
cannot read sealed messages. This is arithmetic, not policy.

## Construction (Noise X pattern)

Sender holds the recipient's Ed25519 identity pubkey and their own
Ed25519 identity keypair.

1. Convert recipient Ed25519 pubkey → X25519 (`PublicKeyToX25519`,
   Edwards→Montgomery via filippo.io/edwards25519). Degenerate points
   (e.g. the identity element) are rejected — a low-order ECDH result
   is an error, never a key.
2. Generate a fresh ephemeral X25519 keypair per message.
3. `shared = X25519(ephemeral_priv, recipient_x25519_pub)`
4. `key = HKDF-SHA256(shared, info="gandr-sealed-v1")`
5. Build the inner plaintext (below), zero-pad to a 1024-byte boundary.
6. Encrypt with XChaCha20-Poly1305 (random 24-byte nonce).

## Inner plaintext layout

```
[sender_pubkey:    32 bytes]   always present (claimed sender)
[inner_signature:  64 bytes]   present only if NOT deniable
[message_type:      1 byte ]
[content_len:       4 bytes]   uint32 big-endian
[content:        variable  ]
[padding:        variable  ]   zeros to the 1024-byte boundary
```

`content_len` makes padding removal unambiguous (content may end in
zeros). Non-zero padding after decryption is treated as corruption and
rejected. Messages of different lengths within the same 1024-byte
bucket produce identical ciphertext sizes.

The inner signature covers:

```
SHA256("gandr-sealed-inner-v1" || recipient_ed25519_pubkey || message_type || content)
```

Binding the recipient prevents a recipient from re-presenting the
signed content as having been sealed to someone else.

## Deniable mode

With `Deniable = true` the inner signature is omitted. The recipient
can read the message but cannot cryptographically prove authorship to
anyone. The flag travels in the (node-visible) payload; flipping it in
transit cannot upgrade an unsigned message to signed — parsing with the
wrong layout fails or yields an unverifiable signature, both rejected.

## Delivery confirmation

`SealedAck (0x11)` carries only `SHA256(sealed message)`, signed by the
recipient identity. The sender learns the message landed; the node
learns timing and nothing else.

## Size limit

Content up to `MaxSealedContent` (63 KiB region bounded by the 65535
envelope payload limit, minus headers and AEAD tag). One block (1024 B)
is the minimum ciphertext size.
