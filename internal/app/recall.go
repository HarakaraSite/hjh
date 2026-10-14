package app

import (
	"fmt"

	"hjh/internal/contracts"
	"hjh/internal/recall"
)

type recallAttribution struct {
	marker            contracts.RecallMarker
	targetExecutionID string
	recordID          string
	sourceLocators    []contracts.ExecutionLocator
}

func recallMessage(projection recall.Projection, reference contracts.RecallReference, targetExecutionID, recordID string) (contracts.Message, recallAttribution, error) {
	if recordID == "" {
		return contracts.Message{}, recallAttribution{}, fmt.Errorf("app recall: admitted execution has no recall relation record")
	}
	text, err := recall.MessageText(projection)
	if err != nil {
		return contracts.Message{}, recallAttribution{}, err
	}
	marker := contracts.RecallMarker{SourceExecutionID: reference.SourceExecutionID,
		RelationRecordID: recordID, OperationID: reference.OperationID, Generation: reference.Generation}
	short := "停止したexecutionの参考情報: " + reference.SourceExecutionID
	message := contracts.User(text)
	message.ExecutionFact = &contracts.ExecutionFact{Kind: "user_message", Role: "user", Text: short,
		Conversation: contracts.ConversationMetadata{EntityKind: "recall", LocalKey: "recall/" + recordID}, Recall: &marker}

	locators := make([]contracts.ExecutionLocator, 0, len(projection.Observations)*2+len(projection.InheritedSources)+1)
	seen := map[string]bool{}
	appendLocator := func(locator contracts.ExecutionLocator) {
		if locator.ExecutionID == "" {
			return
		}
		key := fmt.Sprintf("%s|%s|%d|%d|%d", locator.ExecutionID, locator.RecordID, locator.Ordinal, locator.MessageOrdinal, locator.ItemIndex)
		if seen[key] {
			return
		}
		seen[key] = true
		locators = append(locators, locator)
	}
	appendLocator(contracts.ExecutionLocator{ExecutionID: projection.SourceExecutionID})
	for _, source := range projection.InheritedSources {
		appendLocator(contracts.ExecutionLocator{ExecutionID: source.SourceExecutionID})
	}
	for _, observation := range projection.Observations {
		appendLocator(observation.SourceLocator)
		if observation.ResultSourceLocator != nil {
			appendLocator(*observation.ResultSourceLocator)
		}
		for _, activity := range observation.Activity {
			appendLocator(activity.SourceLocator)
		}
	}
	return message, recallAttribution{marker: marker, targetExecutionID: targetExecutionID,
		recordID: recordID, sourceLocators: locators}, nil
}

func (a recallAttribution) externalRelation() map[string]any {
	return map[string]any{"kind": "recall", "sourceExecutionId": a.marker.SourceExecutionID,
		"targetExecutionId": a.targetExecutionID, "recordId": a.recordID,
		"sourceLocators": a.sourceLocators}
}
