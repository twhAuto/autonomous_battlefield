package providers

import "errors"

var (
	ErrRefusal   = errors.New("model refused the decision request")
	ErrRateLimit = errors.New("model provider rate limit")
)
