package svloc

import (
	"errors"
)

// ErrGetAlreadyCalled ...
var ErrGetAlreadyCalled = errors.New("svloc: method Get already called")
