package authorization

import (
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/Xboxpig/asus-mu3c-ad-m2-es9260/internal/firmware"
)

func TestPermitIsBoundToOneChallenge(t *testing.T) {
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	public := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	challenge := Challenge{
		Purpose:      Purpose,
		AttemptID:    "00112233445566778899aabbccddeeff",
		DeviceID:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BackupSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		TargetSHA256: firmware.V3SHA256,
		CreatedUTC:   now.Add(-10 * time.Minute).Format(time.RFC3339),
	}
	permit, err := Sign(challenge, seed, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(permit, challenge, public, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	other := challenge
	other.AttemptID = "ffeeddccbbaa99887766554433221100"
	if err := Verify(permit, other, public, now.Add(time.Minute)); err == nil {
		t.Fatal("permit accepted another installation attempt")
	}
	permit.Claim.Challenge.BackupSHA256 = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if err := Verify(permit, permit.Claim.Challenge, public, now.Add(time.Minute)); err == nil {
		t.Fatal("permit accepted a changed backup hash")
	}
	permit, _ = Sign(challenge, seed, now)
	if err := Verify(permit, challenge, public, now.Add(Validity+time.Second)); err == nil {
		t.Fatal("expired permit accepted")
	}
}
