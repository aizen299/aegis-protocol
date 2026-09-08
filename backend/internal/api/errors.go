package api

import "errors"

var (
	errInvalidLimit  = errors.New("limit must be a positive integer")
	errInvalidOffset = errors.New("offset must be a non-negative integer")
)
