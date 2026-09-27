package main

import (
	"fmt"
	"os"
	"time"

	"github.com/Xboxpig/asus-mu3c-ad-m2-es9260/internal/authorization"
)

func main() {
	if len(os.Args) != 2 {
		fail("usage: permit-verifier PERMIT.json (CHALLENGE_JSON environment variable required)")
	}
	challenge, err := authorization.DecodeChallenge([]byte(os.Getenv("CHALLENGE_JSON")))
	if err != nil {
		fail("invalid challenge: %v", err)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fail("read permit: %v", err)
	}
	permit, err := authorization.DecodePermit(data)
	if err != nil {
		fail("invalid permit: %v", err)
	}
	publicKey, err := authorization.ProductionPublicKey()
	if err != nil {
		fail("public key: %v", err)
	}
	if err := authorization.Verify(permit, challenge, publicKey, time.Now()); err != nil {
		fail("permit verification: %v", err)
	}
	fmt.Println("Permit signature, target hash and challenge verified")
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
