package runner

import (
	"fmt"
	"regexp"
	"strings"
)

// ErrorCategoryProviderRateLimited marks a CLI run the provider refused for rate or
// usage limits, distinguishing exhausted allowances from defects in the CLI or prompt.
const ErrorCategoryProviderRateLimited = "provider_rate_limited"

const cliFailureDetailRunes = 240

var (
	rateLimitPattern   = regexp.MustCompile(`(?i)\b429\b|too many requests|rate[ -]?limit|usage limit`)
	bearerTokenPattern = regexp.MustCompile(`(?i)bearer\s+\S+`)
)

// cliFailure classifies the CLI's own terminal error message. The returned error
// carries a short single-line detail with bearer credentials redacted, for logs.
func cliFailure(cli, message string) (string, error) {
	detail := CLIFailureDetail(message)
	if rateLimitPattern.MatchString(message) {
		return ErrorCategoryProviderRateLimited, fmt.Errorf("%s was rate limited by its provider: %s", cli, detail)
	}
	return "cli", fmt.Errorf("%s failed: %s", cli, detail)
}

// CLIFailureDetail reduces error text to one bounded line with bearer credentials redacted.
func CLIFailureDetail(message string) string {
	detail := bearerTokenPattern.ReplaceAllString(strings.Join(strings.Fields(message), " "), "Bearer [redacted]")
	if runes := []rune(detail); len(runes) > cliFailureDetailRunes {
		detail = string(runes[:cliFailureDetailRunes]) + "…"
	}
	return detail
}
