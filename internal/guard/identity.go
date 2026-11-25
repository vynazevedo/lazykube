package guard

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
)

var sessionIdentity = regexp.MustCompile(`^session-[0-9]+-[0-9a-f]{32}$`)

func NewSessionIdentity(generation uint64) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return fmt.Sprintf("session-%d-%s", generation, hex.EncodeToString(nonce)), nil
}

func UniqueSessionIdentity(identity string) bool {
	return sessionIdentity.MatchString(identity)
}
