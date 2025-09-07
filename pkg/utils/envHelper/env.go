package envHelper

import (
	"crypto/rand"
	"strings"
)

func ReplaceDefaultENV(key, tz string) string {
	temp := ""
	switch key {
	case "$DefaultPassword":
		temp = "casaos"
	case "$DefaultUserName":
		temp = "admin"

	case "$PUID":
		temp = "1000"
	case "$PGID":
		temp = "1000"
	case "$TZ":
		temp = tz
	case "$AUTH_HASH":
		temp = GenerateAuthHash()
	}
	return temp
}

// GenerateAuthHash generates a secure 128-character random string for AUTH_HASH
func GenerateAuthHash() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	const length = 128
	
	b := make([]byte, length)
	_, err := rand.Read(b)
	if err != nil {
		// Fallback to a basic implementation if crypto/rand fails
		// Generate a 128-character fallback string
		fallback := "casaos_fallback_auth_hash_"
		for len(fallback) < 128 {
			fallback += "0123456789abcdef"
		}
		return fallback[:128]
	}
	
	for i := range b {
		b[i] = chars[b[i]%byte(len(chars))]
	}
	return string(b)
}

// replace env default setting
func ReplaceStringDefaultENV(str string) string {
	return strings.ReplaceAll(strings.ReplaceAll(str, "$DefaultPassword", ReplaceDefaultENV("$DefaultPassword", "")), "$DefaultUserName", ReplaceDefaultENV("$DefaultUserName", ""))
}
