package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
)

func main() {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Stdout is piped directly to `gh secret set`. Never log or save the seed.
	fmt.Println(base64.StdEncoding.EncodeToString(privateKey.Seed()))
	fmt.Fprintln(os.Stderr, "Public key:", base64.StdEncoding.EncodeToString(publicKey))
}
