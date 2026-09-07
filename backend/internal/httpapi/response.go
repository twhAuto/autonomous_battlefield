// Provides strict single-value JSON request decoding and response encoding that avoids
// committing headers before encoding succeeds.

package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const maxJSONRequestBodyBytes = 64 << 10

// ReadJSON decodes one JSON value, rejecting unknown fields, trailing values,
// and request bodies larger than 64 KiB.
func ReadJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONRequestBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain a single JSON value")
	}

	return nil
}

// WriteJSON encodes before committing the status so callers can still send an
// error response if the value cannot be marshaled.
func WriteJSON(w http.ResponseWriter, status int, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, err = w.Write(payload)
	return err
}
