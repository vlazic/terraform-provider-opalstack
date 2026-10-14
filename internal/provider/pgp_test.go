package provider

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// testPGPKey generates a throwaway key pair. No test ever uses a real key.
// Ed25519 with an X25519 subkey, which is quick to make and which gpg reads.
func testPGPKey(t testing.TB, config *packet.Config) *openpgp.Entity {
	t.Helper()
	if config == nil {
		config = &packet.Config{}
	}
	config.Algorithm = packet.PubKeyAlgoEdDSA
	key, err := openpgp.NewEntity("tfacc", "throwaway key of a test", "tfacc@example.com", config)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// armored writes a key's public half, or with private set the whole key, ASCII-armored.
func armored(t testing.TB, key *openpgp.Entity, private bool) string {
	t.Helper()
	var out bytes.Buffer
	blockType, serialize := openpgp.PublicKeyType, key.Serialize
	if private {
		blockType, serialize = openpgp.PrivateKeyType, func(w io.Writer) error { return key.SerializePrivate(w, nil) }
	}
	w, err := armor.Encode(&out, blockType, nil)
	if err == nil {
		err = serialize(w)
	}
	if err == nil {
		err = w.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// binaryBase64 is a key's public half in the other accepted form.
func binaryBase64(t testing.TB, key *openpgp.Entity) string {
	t.Helper()
	var out bytes.Buffer
	if err := key.Serialize(&out); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(out.Bytes())
}

// pgpDecrypt reads a message pgpEncrypt wrote, with the private key it was encrypted to.
func pgpDecrypt(key *openpgp.Entity, message string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(message)
	if err != nil {
		return "", err
	}
	md, err := openpgp.ReadMessage(bytes.NewReader(raw), openpgp.EntityList{key}, nil, nil)
	if err != nil {
		return "", err
	}
	text, err := io.ReadAll(md.UnverifiedBody)
	return string(text), err
}

func TestPGPEncryptsToAPublicKey(t *testing.T) {
	key := testPGPKey(t, nil)
	other := testPGPKey(t, nil)
	for form, text := range map[string]string{
		"armored":               armored(t, key, false),
		"base64":                binaryBase64(t, key),
		"base64, wrapped lines": wrap(binaryBase64(t, key), 64),
	} {
		t.Run(form, func(t *testing.T) {
			public, err := pgpPublicKey(text, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if public.PrivateKey != nil {
				t.Fatal("the parsed key holds private material")
			}
			if got, want := pgpFingerprint(public), fmt.Sprintf("%X", key.PrimaryKey.Fingerprint); got != want || len(got) != 40 {
				t.Errorf("fingerprint %s, want %s", got, want)
			}
			message, err := pgpEncrypt(public, "the-generated-password")
			if err != nil {
				t.Fatal(err)
			}
			if got, err := pgpDecrypt(key, message); err != nil || got != "the-generated-password" {
				t.Errorf("decrypted %q, %v", got, err)
			}
			if _, err := pgpDecrypt(other, message); err == nil {
				t.Error("another key decrypted the message")
			}
		})
	}
}

func TestPGPRefusesUnusableKeys(t *testing.T) {
	now := time.Now()
	key := testPGPKey(t, nil)

	expired := testPGPKey(t, &packet.Config{Time: func() time.Time { return now.Add(-48 * time.Hour) }, KeyLifetimeSecs: 3600})

	revoked := testPGPKey(t, nil)
	if err := revoked.RevokeKey(packet.KeyRetired, "", nil); err != nil {
		t.Fatal(err)
	}

	signingOnly := testPGPKey(t, nil)
	signingOnly.Subkeys = nil

	var both bytes.Buffer
	for _, k := range []*openpgp.Entity{key, testPGPKey(t, nil)} {
		if err := k.Serialize(&both); err != nil {
			t.Fatal(err)
		}
	}

	for name, c := range map[string]struct{ text, want string }{
		"private key":       {armored(t, key, true), "is a private key"},
		"garbage":           {"not a key!", "neither an ASCII-armored key nor base64"},
		"base64 of garbage": {base64.StdEncoding.EncodeToString([]byte("not a key at all")), "not a readable OpenPGP key"},
		"broken armor":      {strings.Replace(armored(t, key, false), "\n\n", "\n\n!!", 1), "not a readable OpenPGP key"},
		"a message":         {"-----BEGIN PGP MESSAGE-----\n\nwcBMA\n-----END PGP MESSAGE-----\n", "not a readable OpenPGP key"},
		"expired":           {armored(t, expired, false), "has expired"},
		"revoked":           {armored(t, revoked, false), "has been revoked"},
		"signing only":      {armored(t, signingOnly, false), "no valid subkey that can encrypt"},
		"two keys":          {base64.StdEncoding.EncodeToString(both.Bytes()), "holds 2 keys"},
		"two armored keys":  {armored(t, key, false) + armored(t, key, false), "more than one armored block"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := pgpPublicKey(c.text, now)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error saying %q", err, c.want)
			}
			if len(c.text) > 40 && strings.Contains(err.Error(), c.text[20:40]) {
				t.Errorf("the error quotes the key: %v", err)
			}
		})
	}

	// The key that expired was usable while it lasted.
	if _, err := pgpPublicKey(armored(t, expired, false), now.Add(-47*time.Hour)); err != nil {
		t.Errorf("refused before it expired: %v", err)
	}
}

func wrap(text string, width int) string {
	var out strings.Builder
	for len(text) > width {
		out.WriteString(text[:width] + "\n")
		text = text[width:]
	}
	return out.String() + text + "\n"
}
