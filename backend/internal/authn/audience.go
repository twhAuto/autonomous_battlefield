package authn

import (
	"encoding/json"
	"errors"
)

// accessAudience accepts the two audience representations in RFC 7519 section 4.1.3.
// Values remain unchanged for exact, case-sensitive audience matching.
type accessAudience []string

func (a *accessAudience) UnmarshalJSON(data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	switch value := value.(type) {
	case string:
		*a = accessAudience{value}
	case []any:
		audiences := make(accessAudience, len(value))
		for i, item := range value {
			text, ok := item.(string)
			if !ok {
				return errors.New("audience array must contain only strings")
			}
			audiences[i] = text
		}
		*a = audiences
	default:
		return errors.New("audience must be a string or an array of strings")
	}
	return nil
}
