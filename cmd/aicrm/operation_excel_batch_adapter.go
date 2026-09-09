package main

import (
	"context"
	"encoding/json"
	"fmt"

	operationport "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/port"
)

// operationCycleExcelStrategyAdapter is deliberately a composition-root
// adapter. AI Assistant sees only this stable read Port and never imports an
// OperationCycle store or table. The underlying repository receives the
// caller's already-bound transaction, so association validation cannot split
// from the Excel batch write.
type operationCycleExcelStrategyAdapter struct {
	read interface {
		GetStrategy(context.Context, string) (map[string]any, error)
	}
}

func (a operationCycleExcelStrategyAdapter) OperationCycleStrategy(ctx context.Context, key string) (operationport.Strategy, error) {
	if a.read == nil {
		return operationport.Strategy{}, fmt.Errorf("operation-cycle strategy reader unavailable")
	}
	value, err := a.read.GetStrategy(ctx, key)
	if err != nil {
		return operationport.Strategy{}, err
	}
	strategyKey, _ := value["strategy_key"].(string)
	title, _ := value["title"].(string)
	status, _ := value["status"].(string)
	version := 0
	switch raw := value["version"].(type) {
	case int:
		version = raw
	case int32:
		version = int(raw)
	case int64:
		version = int(raw)
	case float64:
		version = int(raw)
	}
	definition, definitionErr := json.Marshal(value["definition"])
	snapshot, snapshotErr := json.Marshal(value["snapshot"])
	if strategyKey != key || title == "" || status == "" || version < 1 || definitionErr != nil || snapshotErr != nil {
		return operationport.Strategy{}, fmt.Errorf("operation-cycle strategy projection invalid")
	}
	return operationport.Strategy{Key: strategyKey, Title: title, Status: status, Version: version, Definition: definition, Snapshot: snapshot}, nil
}

var _ operationport.StrategyReader = operationCycleExcelStrategyAdapter{}
