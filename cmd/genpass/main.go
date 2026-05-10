package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: genpass <password>")
		fmt.Fprintln(os.Stderr, "prints bcrypt hash for password_hash and a fresh session_secret")
		os.Exit(1)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(os.Args[1]), 12)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hash error:", err)
		os.Exit(1)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		fmt.Fprintln(os.Stderr, "rand error:", err)
		os.Exit(1)
	}
	fmt.Printf("password_hash:  %s\n", string(hash))
	fmt.Printf("session_secret: %s\n", hex.EncodeToString(secret))
}