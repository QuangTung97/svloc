package svloc

import (
	"errors"
)

var ErrGetAlreadyCalled = errors.New("svloc: method Get already called")
