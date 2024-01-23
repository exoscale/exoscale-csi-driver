package driver

import "errors"

var (
	errLimitLessThanRequiredBytes      = errors.New("limit size is less than required size")
	errRequiredBytesLessThanMinimun    = errors.New("required size is less than the minimun size")
	errLimitLessThanMinimum            = errors.New("limit size is less than the minimun size")
	errRequiredBytesGreaterThanMaximun = errors.New("required size is greater than the maximum size")
	errLimitGreaterThanMaximum         = errors.New("limit size is greater than the maximum size")
)
