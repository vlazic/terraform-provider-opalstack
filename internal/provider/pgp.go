package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// A password the API chooses is reported once, in the answer to a create. With
// pgp_key set it is kept only encrypted to that key, in the format AWS uses for
// the same purpose: base64 of a binary OpenPGP message, so that
// `base64 -d | gpg -d` reads it.

// pgpPublicKey reads one public OpenPGP key, ASCII-armored or as base64 of the
// binary form, and refuses one that cannot encrypt to anyone at time now. The
// errors never quote the key.
func pgpPublicKey(text string, now time.Time) (*openpgp.Entity, error) {
	var keys openpgp.EntityList
	var err error
	switch blocks := strings.Count(text, "-----BEGIN PGP"); {
	case blocks > 1:
		return nil, errors.New("it holds more than one armored block; give exactly one key")
	case blocks == 1:
		keys, err = openpgp.ReadArmoredKeyRing(strings.NewReader(text))
	default:
		var raw []byte
		raw, err = base64.StdEncoding.DecodeString(strings.Join(strings.Fields(text), ""))
		if err != nil {
			return nil, errors.New("it is neither an ASCII-armored key nor base64 of a binary one")
		}
		keys, err = openpgp.ReadKeyRing(bytes.NewReader(raw))
	}
	switch {
	case err != nil:
		return nil, fmt.Errorf("it is not a readable OpenPGP key: %w", err)
	case len(keys) == 0:
		return nil, errors.New("it holds no OpenPGP key")
	case len(keys) > 1:
		return nil, fmt.Errorf("it holds %d keys; give exactly one", len(keys))
	}
	key := keys[0]
	if key.PrivateKey != nil {
		return nil, errors.New("it is a private key; give the public key only, as `gpg --export` writes it")
	}
	selfSig, identity := key.PrimarySelfSignature()
	switch {
	case key.Revoked(now) || (identity != nil && identity.Revoked(now)):
		return nil, errors.New("the key has been revoked")
	case selfSig != nil && (key.PrimaryKey.KeyExpired(selfSig, now) || selfSig.SigExpired(now)):
		return nil, errors.New("the key has expired")
	}
	if _, ok := key.EncryptionKey(now); !ok {
		return nil, errors.New("the key has no valid subkey that can encrypt; a signing-only key, or one whose encryption subkeys expired or were revoked, cannot be used")
	}
	return key, nil
}

// pgpFingerprint is the fingerprint of a key's primary key, in the upper-case
// hexadecimal `gpg --fingerprint` shows without its spaces.
func pgpFingerprint(key *openpgp.Entity) string {
	return strings.ToUpper(hex.EncodeToString(key.PrimaryKey.Fingerprint))
}

// pgpEncrypt encrypts a text to a key and returns the binary message, base64 encoded.
func pgpEncrypt(key *openpgp.Entity, text string) (string, error) {
	var out bytes.Buffer
	w, err := openpgp.Encrypt(&out, []*openpgp.Entity{key}, nil, nil, nil)
	if err != nil {
		return "", err
	}
	if _, err := w.Write([]byte(text)); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(out.Bytes()), nil
}

// pgpKeyValidator refuses at plan time a pgp_key that the create would refuse.
type pgpKeyValidator struct{}

func (pgpKeyValidator) Description(context.Context) string {
	return "Must be one public OpenPGP key that can encrypt."
}

func (v pgpKeyValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (pgpKeyValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := pgpPublicKey(req.ConfigValue.ValueString(), time.Now()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Unusable pgp_key", "The pgp_key cannot be used: "+err.Error()+".")
	}
}
