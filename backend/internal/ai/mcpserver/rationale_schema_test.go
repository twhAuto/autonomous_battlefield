package mcpserver

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/game/controller"
)

func TestRationaleSchemaStatesLimit(t *testing.T) {
	field, _ := reflect.TypeOf(submitPlanInput{}).FieldByName("Rationale")
	if !strings.Contains(field.Tag.Get("jsonschema"), fmt.Sprintf("at most %d characters", controller.MaxRationaleCharacters)) {
		t.Fatal("submit_strategy_plan must state the rationale limit")
	}
}
