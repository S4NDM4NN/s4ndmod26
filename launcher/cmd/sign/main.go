// sign writes <file>.sig (base64 ed25519 signature over the file's sha256) for each file given.
//
//	go run ./cmd/sign -key /run/secrets/launcher_signing_key dist/s4ndmod-launcher.exe ...
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"

	"s4ndmod26/launcher/internal/sig"
)

func main() {
	keyFile := flag.String("key", "", "private key file written by keygen (required)")
	flag.Parse()
	if *keyFile == "" || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: sign -key <private key file> <file>...")
		os.Exit(2)
	}
	text, err := os.ReadFile(*keyFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	priv, err := sig.ParsePrivateKey(string(text))
	if err != nil {
		fmt.Fprintln(os.Stderr, "key:", err)
		os.Exit(1)
	}
	for _, p := range flag.Args() {
		f, err := os.Open(p)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		sum := hex.EncodeToString(h.Sum(nil))
		if err := os.WriteFile(p+".sig", []byte(sig.Sign(priv, sum)+"\n"), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("signed %s (%s)\n", p, sum[:12])
	}
}
