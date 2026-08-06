package settings

import "errors"

// errInvalidSetting wraps every Validate failure, so a caller can tell a
// rejected value from a storage error with errors.Is.
var errInvalidSetting = errors.New("invalid setting")
