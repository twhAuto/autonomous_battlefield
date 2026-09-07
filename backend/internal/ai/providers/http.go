package providers

import (
	"encoding/json"
	"fmt"
	"net/http"
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
func doJSON(client *http.Client, request *http.Request, provider string, output any) error {
	if client == nil {
		client = telemetry.HTTPClient(nil)
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("%w: %s response status %d", ErrRateLimit, provider, response.StatusCode)
	}
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("%s response status %d", provider, response.StatusCode)
	}
	return json.NewDecoder(response.Body).Decode(output)
}
