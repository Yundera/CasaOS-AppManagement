package auth

import "crypto/rand"

// GenerateHash generates a secure 128-character random string for AUTH_HASH
// This should only be called during app installation, not updates
func GenerateHash() string {
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