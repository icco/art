package settings

import "errors"

// ErrInvalidSetting wraps every Validate failure, so a caller can tell a
// rejected value from a storage error with errors.Is.
var ErrInvalidSetting = errors.New("invalid setting")

var errInvalidSetting = ErrInvalidSetting
