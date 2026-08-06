package oauth

import "errors"

// Failures in the OAuth dance and in token storage.
var (
	errKeySize        = errors.New("oauth: key must be 32 bytes")
	errShortCipher    = errors.New("oauth: ciphertext too short")
	errUnknownAccount = errors.New("oauth: unknown account kind")
	errUnknownState   = errors.New("oauth: unknown or expired state")
	errStateExpired   = errors.New("oauth: state expired")
	errNoRefreshToken = errors.New("oauth: refresh token missing")
)
