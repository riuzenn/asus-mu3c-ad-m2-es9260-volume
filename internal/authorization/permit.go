package authorization

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Xboxpig/asus-mu3c-ad-m2-es9260/internal/firmware"
)

const (
	Purpose         = "asus-mu3c-ad-m2-es9260/windows-flash/v1"
	Validity        = 4 * time.Hour
	MaxChallengeAge = 24 * time.Hour
	// The private seed is stored only in the owner's GitHub Actions secret.
	PublicKeyBase64 = "Ay4kSrTcCeeGtH/AJPHV/uLPsmDyn/uo8187VuA0UNc="
)

type Challenge struct {
	Purpose      string `json:"purpose"`
	AttemptID    string `json:"attempt_id"`
	DeviceID     string `json:"device_id_sha256"`
	BackupSHA256 string `json:"backup_sha256"`
	TargetSHA256 string `json:"target_sha256"`
	CreatedUTC   string `json:"created_utc"`
}

type Claim struct {
	Challenge  Challenge `json:"challenge"`
	IssuedUTC  string    `json:"issued_utc"`
	ExpiresUTC string    `json:"expires_utc"`
}

type Permit struct {
	Claim     Claim  `json:"claim"`
	Signature string `json:"signature_ed25519_base64"`
}

func decodeStrict(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

func DecodeChallenge(data []byte) (Challenge, error) {
	var challenge Challenge
	err := decodeStrict(data, &challenge)
	return challenge, err
}

func DecodePermit(data []byte) (Permit, error) {
	var permit Permit
	err := decodeStrict(data, &permit)
	return permit, err
}

func validHex(value string, bytes int) bool {
	if len(value) != bytes*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func ValidateChallenge(challenge Challenge, now time.Time) error {
	if challenge.Purpose != Purpose || !validHex(challenge.AttemptID, 16) ||
		!validHex(challenge.DeviceID, 32) || !validHex(challenge.BackupSHA256, 32) ||
		challenge.TargetSHA256 != firmware.V3SHA256 {
		return errors.New("challenge purpose, identity, or target hash invalid")
	}
	created, err := time.Parse(time.RFC3339, challenge.CreatedUTC)
	if err != nil || created.Location() != time.UTC ||
		created.After(now.Add(5*time.Minute)) || now.Sub(created) > MaxChallengeAge {
		return errors.New("challenge timestamp invalid or stale")
	}
	return nil
}

func claimMessage(claim Claim) ([]byte, error) {
	return json.Marshal(claim)
}

func Sign(challenge Challenge, seed []byte, now time.Time) (Permit, error) {
	if len(seed) != ed25519.SeedSize {
		return Permit{}, errors.New("Ed25519 seed must be exactly 32 bytes")
	}
	if err := ValidateChallenge(challenge, now); err != nil {
		return Permit{}, err
	}
	claim := Claim{
		Challenge:  challenge,
		IssuedUTC:  now.UTC().Format(time.RFC3339),
		ExpiresUTC: now.UTC().Add(Validity).Format(time.RFC3339),
	}
	message, err := claimMessage(claim)
	if err != nil {
		return Permit{}, err
	}
	signature := ed25519.Sign(ed25519.NewKeyFromSeed(seed), message)
	return Permit{Claim: claim, Signature: base64.StdEncoding.EncodeToString(signature)}, nil
}

func Verify(permit Permit, expected Challenge, publicKey ed25519.PublicKey, now time.Time) error {
	if err := ValidateChallenge(expected, now); err != nil {
		return err
	}
	if permit.Claim.Challenge != expected {
		return errors.New("permit is for a different installation attempt")
	}
	issued, issuedErr := time.Parse(time.RFC3339, permit.Claim.IssuedUTC)
	expires, expiresErr := time.Parse(time.RFC3339, permit.Claim.ExpiresUTC)
	if issuedErr != nil || expiresErr != nil || issued.After(now.Add(5*time.Minute)) ||
		expires.Before(now) || expires.Sub(issued) > Validity || expires.Sub(issued) <= 0 {
		return errors.New("permit timestamp invalid or expired")
	}
	signature, err := base64.StdEncoding.DecodeString(permit.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || len(publicKey) != ed25519.PublicKeySize {
		return errors.New("permit signature or public key invalid")
	}
	message, err := claimMessage(permit.Claim)
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, message, signature) {
		return errors.New("upstream permit signature invalid")
	}
	return nil
}

func ProductionPublicKey() (ed25519.PublicKey, error) {
	decoded, err := base64.StdEncoding.DecodeString(PublicKeyBase64)
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("installer public key has not been provisioned")
	}
	return ed25519.PublicKey(decoded), nil
}
