package ai

import "math"

// MetadataMilliseconds accepts only finite nonnegative worker-reported durations.
func MetadataMilliseconds(metadata map[string]any, key string) *int64 {
	switch value := metadata[key].(type) {
	case int64:
		if value >= 0 {
			return &value
		}
	case float64:
		if value >= 0 && value < math.MaxInt64 && math.Trunc(value) == value {
			result := int64(value)
			return &result
		}
	}
	return nil
}
