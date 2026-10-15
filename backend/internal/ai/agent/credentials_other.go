//go:build !linux

package agent

import "errors"

// ProtectCredentials refuses production on platforms without Linux process isolation.
func ProtectCredentials(production bool) error {
	if production {
		return errors.New("production workers require Linux process isolation")
	}
	return nil
}
