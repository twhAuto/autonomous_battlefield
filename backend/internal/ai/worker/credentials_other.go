//go:build !linux

package worker

import (
	"errors"
	"os"
)

func ProtectCredentials() error {
	if os.Getenv("BATTLEFIELD_ENV") == "production" {
		return errors.New("production workers require Linux process isolation")
	}
	return nil
}
