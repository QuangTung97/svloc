package svloc

import (
	"errors"
)

// ErrGetAlreadyCalled ...
var ErrGetAlreadyCalled = errors.New("svloc: method Get already called")

var errOverrideInsideNewFunctions = errors.New("svloc: method Override must NOT be called inside new functions")
