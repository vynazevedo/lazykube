package app

import "errors"

var (
	errResourceNotFound   = errors.New("resource not found")
	errNoResourceSelected = errors.New("no resource selected")
)
