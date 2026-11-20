package mcpserver

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/game"
)

func TestProductionOrdersThroughMCP(t *testing.T) {
	repository := &fakeRepository{record: ai.DecisionRecord{ID: "repeat", FactionID: "a"}}
	repository.record.Observation = ai.DecisionContext{
		ContinuousProduction: true, Opening: true,
		Legal: ai.LegalChoices{ActionKinds: []string{"attack"}, RecruitmentSources: []string{"a:castle"}},
		View: game.View{State: game.State{Buildings: []game.Building{
			{ID: "a:castle", FactionID: "a", Health: 100, Trains: []string{"vanguard"}},
		}}},
	}
	service := New(repository, "secret", nil)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := service.mcpServer().Connect(context.WithValue(context.Background(), requestIDKey{}, "repeat"), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for _, enabled := range []bool{true, false} {
		order := map[string]any{"buildingId": "a:castle", "enabled": enabled}
		if enabled {
			order["unitType"] = "vanguard"
		}
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "submit_strategy_plan", Arguments: map[string]any{"requestId": "repeat", "idempotencyKey": "once", "expectedVersion": 0, "targetTick": 0, "name": "repeat", "rationale": "Configure the castle.", "actionWeights": map[string]int{}, "reserveGold": 0, "spendOrder": []string{}, "constructionPriorities": []any{}, "recruitmentPriorities": []string{}, "productionOrders": []any{order}}})
		if err != nil || result.IsError {
			t.Fatal(result, err)
		}
		if repository.submitted.Plan.ProductionOrders == nil || (*repository.submitted.Plan.ProductionOrders)[0].Enabled != enabled {
			t.Fatal("production order lost")
		}
	}
}
