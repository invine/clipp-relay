// Package issuer contains the private Fosite boundary used by Clipp Relay.
// HTTP handlers and callers exchange opaque strings; Fosite types stay here.
package issuer

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

type Purpose string

const (
	AuthorizationCode Purpose = "authorization_code"
	AccessToken       Purpose = "access_token"
	RefreshToken      Purpose = "refresh_token"
)

const credentialNamespace = "clipp-relay/fosite/v1/"

var errInvalidSignature = errors.New("invalid native credential signature")

// LookupDigest maps Fosite's base64url HMAC signature to the fixed-size,
// purpose-separated identifier persisted by typed credential records.
func LookupDigest(purpose Purpose, signature string) ([32]byte, error) {
	var zero [32]byte
	if purpose != AuthorizationCode && purpose != AccessToken && purpose != RefreshToken {
		return zero, errors.New("invalid native credential purpose")
	}
	if len(signature) != 43 {
		return zero, errInvalidSignature
	}
	decoded, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || len(decoded) != 32 {
		return zero, errInvalidSignature
	}
	input := credentialNamespace + string(purpose) + "\x00" + signature
	return sha256.Sum256([]byte(input)), nil
}
