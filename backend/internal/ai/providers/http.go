// Provides shared transcript rendering and HTTP response handling for model-provider
// adapters.

package providers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/telemetry"
)

func modelTranscript(turn ai.ModelTurnRequest) string {
	var transcript strings.Builder
	transcript.WriteString(turn.SystemPrompt)
	transcript.WriteByte('\n')
	for _, message := range turn.Messages {
		transcript.WriteString(strings.ToUpper(message.Role))
		transcript.WriteString(": ")
		transcript.WriteString(message.Content)
		transcript.WriteByte('\n')
	}
	return transcript.String()
}

// doJSON owns the shared response lifecycle; adapters own provider wire formats.
// maxResponseBytes bounds a provider response in memory, matching the CLI runner's
// output limit, so a misbehaving endpoint cannot exhaust the agent.
const maxResponseBytes = 4 << 20

func doJSON(client *http.Client, request *http.Request, provider string, output any) error {
	if client == nil {
		client = telemetry.HTTPClient(nil)
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body := io.LimitReader(response.Body, maxResponseBytes+1)
	if response.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("%w: %s response status %d%s", ErrRateLimit, provider, response.StatusCode, errorKind(body))
	}
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("%s response status %d%s", provider, response.StatusCode, errorKind(body))
	}
	payload, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if len(payload) > maxResponseBytes {
		return fmt.Errorf("%s response exceeds %d bytes", provider, maxResponseBytes)
	}
	return json.Unmarshal(payload, output)
}

var errorToken = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// errorKind reports only a provider's machine-readable error class, such as OpenAI's
// type and code or Gemini's status, and never its free-text message, which can echo
// request content. It returns "" when the body has no recognizable class.
func errorKind(body io.Reader) string {
	var decoded struct {
		Error struct {
			Type   string          `json:"type"`
			Status string          `json:"status"`
			Code   json.RawMessage `json:"code"`
		} `json:"error"`
	}
	if json.NewDecoder(body).Decode(&decoded) != nil {
		return ""
	}
	var kinds []string
	for _, value := range []string{decoded.Error.Type, decoded.Error.Status, strings.Trim(string(decoded.Error.Code), `"`)} {
		if errorToken.MatchString(value) {
			kinds = append(kinds, value)
		}
	}
	if len(kinds) == 0 {
		return ""
	}
	return " (" + strings.Join(kinds, "/") + ")"
}
