package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Xboxpig/asus-mu3c-ad-m2-es9260/internal/authorization"
)

func main() {
	if len(os.Args) != 2 {
		fail("usage: permit-signer OUTPUT.json (CHALLENGE_JSON and AUTH_SIGNING_SEED environment variables required)")
	}
	seed, err := base64.StdEncoding.DecodeString(os.Getenv("AUTH_SIGNING_SEED"))
	if err != nil || len(seed) != ed25519.SeedSize {
		fail("AUTH_SIGNING_SEED is unavailable or invalid")
	}
	challenge, err := authorization.DecodeChallenge([]byte(os.Getenv("CHALLENGE_JSON")))
	if err != nil {
		fail("invalid challenge: %v", err)
	}
	permit, err := authorization.Sign(challenge, seed, time.Now())
	if err != nil {
		fail("cannot issue permit: %v", err)
	}
	data, err := json.MarshalIndent(permit, "", "  ")
	if err != nil {
		fail("encode permit: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(os.Args[1], data, 0600); err != nil {
		fail("write permit: %v", err)
	}
	fmt.Println("Permit created for attempt", challenge.AttemptID)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
