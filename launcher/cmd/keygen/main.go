// keygen creates an ed25519 keypair for signing launcher updates.
//
//	go run ./cmd/keygen -out launcher-signing.key
//
// The private key is written (mode 0600) to the file and never printed.  The public key is printed
// as the line to add to launcher/signing_keys.txt.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
)

func main() {
	out := flag.String("out", "", "file to write the private key to (required)")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "usage: keygen -out <private key file>")
		os.Exit(2)
	}
	if _, err := os.Stat(*out); err == nil {
		fmt.Fprintln(os.Stderr, "refusing to overwrite", *out)
		os.Exit(1)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(base64.StdEncoding.EncodeToString(pub))
}
