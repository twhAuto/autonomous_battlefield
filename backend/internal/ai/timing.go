package ai

import "math"

// ContextDelivery exposes only recognized delivery modes, never arbitrary metadata.
func ContextDelivery(metadata map[string]any) string {
	value, _ := metadata["contextDelivery"].(string)
	if value == "prefetch" || value == "tool" {
		return value
	}
	return ""
}

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
