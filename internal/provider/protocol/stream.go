package protocol

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"hjh/internal/contracts"
)

var errStopSSE = errors.New("stop SSE reader")

type streamAssembly struct {
	api              string
	text             strings.Builder
	chatReasoning    strings.Builder
	chatReasoningRaw []json.RawMessage
	thinking         map[string]*strings.Builder
	thinkingKind     map[string]string
	thinkingOrder    []string
	toolCalls        map[int]*toolCallAssembly
	outputItems      map[int]json.RawMessage
	replayItems      []json.RawMessage
	usage            json.RawMessage
	responseID       string
	responseModel    string
	finishReason     string
	terminal         string
	incompleteReason string
	failure          string
	chatDone         bool
	responseDone     bool
	doneMarker       bool
	sawTextDelta     bool
}

type toolCallAssembly struct {
	callID    string
	name      string
	arguments strings.Builder
	finalArgs string
}

func newStreamAssembly(api string) *streamAssembly {
	return &streamAssembly{
		api:          api,
		toolCalls:    make(map[int]*toolCallAssembly),
		outputItems:  make(map[int]json.RawMessage),
		thinking:     make(map[string]*strings.Builder),
		thinkingKind: make(map[string]string),
	}
}

func consumeSSE(reader io.Reader, onData func([]byte) error) error {
	buffered := bufio.NewReader(reader)
	dataLines := make([]string, 0, 1)
	dispatch := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		data := []byte(strings.Join(dataLines, "\n"))
		dataLines = dataLines[:0]
		return onData(data)
	}
	processLine := func(line []byte) error {
		if len(line) == 0 {
			return dispatch()
		}
		if line[0] == ':' {
			return nil
		}
		colon := bytes.IndexByte(line, ':')
		field := line
		value := []byte(nil)
		if colon >= 0 {
			field = line[:colon]
			value = line[colon+1:]
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
		}
		if bytes.Equal(field, []byte("data")) {
			dataLines = append(dataLines, string(value))
		}
		return nil
	}
	for {
		line, err := buffered.ReadBytes('\n')
		if len(line) > 0 {
			if line[len(line)-1] == '\n' {
				line = line[:len(line)-1]
			}
			line = bytes.TrimSuffix(line, []byte{'\r'})
			if processErr := processLine(line); processErr != nil {
				if errors.Is(processErr, errStopSSE) {
					return nil
				}
				return processErr
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return err
			}
			if len(line) == 0 {
				dispatchErr := dispatch()
				if errors.Is(dispatchErr, errStopSSE) {
					return nil
				}
				return dispatchErr
			}
			// A non-newline-terminated final line has already been processed above.
			dispatchErr := dispatch()
			if errors.Is(dispatchErr, errStopSSE) {
				return nil
			}
			return dispatchErr
		}
	}
}

func (assembly *streamAssembly) addEvent(provider *Provider, raw []byte, onProgress func(string, []contracts.ThinkingItem) error) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("[DONE]")) {
		assembly.doneMarker = true
		if assembly.api == "chat-completions" {
			assembly.chatDone = true
		}
		return errStopSSE
	}
	if assembly.api == "chat-completions" {
		return assembly.addChatEvent(raw, provider.config.Options.ReasoningField, onProgress)
	}
	return assembly.addResponsesEvent(raw, onProgress)
}

func (assembly *streamAssembly) addChatEvent(raw []byte, reasoningField string, onProgress func(string, []contracts.ThinkingItem) error) error {
	var event map[string]json.RawMessage
	if err := json.Unmarshal(raw, &event); err != nil {
		return fmt.Errorf("invalid Chat Completions stream JSON: %w", err)
	}
	if value := event["error"]; presentJSON(value) {
		assembly.terminal = "failed"
		assembly.failure = errorDescription(value)
		return errors.New(assembly.failure)
	}
	if value, ok := decodeJSONString(event["model"]); ok && value != "" {
		assembly.responseModel = value
	}
	if value, ok := decodeJSONString(event["id"]); ok && value != "" {
		assembly.responseID = value
	}
	if presentJSON(event["usage"]) {
		assembly.usage = append(assembly.usage[:0], event["usage"]...)
	}
	var choices []json.RawMessage
	if json.Unmarshal(event["choices"], &choices) != nil {
		return nil
	}
	for _, rawChoice := range choices {
		var choice map[string]json.RawMessage
		if json.Unmarshal(rawChoice, &choice) != nil {
			continue
		}
		if index, ok := decodeJSONInt(choice["index"]); ok && index != 0 {
			continue
		}
		if finish, ok := decodeJSONString(choice["finish_reason"]); ok && finish != "" {
			assembly.finishReason = finish
			if finish == "error" {
				assembly.terminal = "failed"
				assembly.failure = "Chat Completions stream ended with finish_reason=error"
				return errors.New(assembly.failure)
			}
		}
		var delta map[string]json.RawMessage
		if json.Unmarshal(choice["delta"], &delta) == nil {
			if text := decodeContent(delta["content"]); text != "" {
				assembly.text.WriteString(text)
				assembly.sawTextDelta = true
				if err := onProgress(assembly.text.String(), nil); err != nil {
					return err
				}
			}
			if thinking := assembly.addChatReasoning(delta, reasoningField); thinking != nil {
				if err := onProgress("", []contracts.ThinkingItem{*thinking}); err != nil {
					return err
				}
			}
			assembly.addChatToolCallFragments(delta["tool_calls"])
		}
	}
	return nil
}

func (assembly *streamAssembly) addChatReasoning(delta map[string]json.RawMessage, field string) *contracts.ThinkingItem {
	if field == "" {
		return nil
	}
	if value, ok := decodeJSONString(delta[field]); ok {
		assembly.chatReasoning.WriteString(value)
		if value == "" {
			return nil
		}
		item := assembly.appendThinking("reasoning", "reasoning", value)
		return &item
	}
	var details []json.RawMessage
	if json.Unmarshal(delta[field], &details) == nil {
		for _, item := range details {
			assembly.chatReasoningRaw = append(assembly.chatReasoningRaw, append(json.RawMessage(nil), item...))
		}
	}
	return nil
}

func (assembly *streamAssembly) addChatToolCallFragments(raw json.RawMessage) {
	var fragments []json.RawMessage
	if json.Unmarshal(raw, &fragments) != nil {
		return
	}
	for position, rawFragment := range fragments {
		var fragment map[string]json.RawMessage
		if json.Unmarshal(rawFragment, &fragment) != nil {
			continue
		}
		index, ok := decodeJSONInt(fragment["index"])
		if !ok {
			index = position
		}
		call := assembly.toolCalls[index]
		if call == nil {
			call = &toolCallAssembly{}
			assembly.toolCalls[index] = call
		}
		if id, ok := decodeJSONString(fragment["id"]); ok && id != "" {
			call.callID = id
		}
		var function map[string]json.RawMessage
		if json.Unmarshal(fragment["function"], &function) != nil {
			continue
		}
		if name, ok := decodeJSONString(function["name"]); ok {
			call.name += name
		}
		if arguments, ok := decodeJSONString(function["arguments"]); ok {
			call.arguments.WriteString(arguments)
		}
	}
}

func (assembly *streamAssembly) addResponsesEvent(raw []byte, onProgress func(string, []contracts.ThinkingItem) error) error {
	var event map[string]json.RawMessage
	if err := json.Unmarshal(raw, &event); err != nil {
		return fmt.Errorf("invalid Responses stream JSON: %w", err)
	}
	typeName, _ := decodeJSONString(event["type"])
	switch typeName {
	case "error":
		assembly.terminal = "failed"
		if presentJSON(event["error"]) {
			assembly.failure = errorDescription(event["error"])
		} else {
			assembly.failure = errorDescription(raw)
		}
		return errStopSSE
	case "response.failed":
		assembly.terminal = "failed"
		var envelope map[string]json.RawMessage
		_ = json.Unmarshal(event["response"], &envelope)
		assembly.failure = errorDescription(envelope["error"])
		return errStopSSE
	case "response.incomplete":
		assembly.terminal = "incomplete"
		var envelope map[string]json.RawMessage
		_ = json.Unmarshal(event["response"], &envelope)
		assembly.incompleteReason = incompleteDescription(envelope["incomplete_details"])
		if presentJSON(envelope["output"]) {
			if err := assembly.captureResponsesOutput(envelope["output"]); err != nil {
				return err
			}
		} else {
			assembly.captureCompletedOutputItems()
		}
		return errStopSSE
	case "response.completed":
		assembly.terminal = "completed"
		assembly.responseDone = true
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(event["response"], &envelope); err != nil {
			return fmt.Errorf("decode completed Responses event: %w", err)
		}
		assembly.responseID, _ = decodeJSONString(envelope["id"])
		assembly.responseModel, _ = decodeJSONString(envelope["model"])
		if presentJSON(envelope["usage"]) {
			assembly.usage = append(assembly.usage[:0], envelope["usage"]...)
		}
		if presentJSON(envelope["output"]) {
			if err := assembly.captureResponsesOutput(envelope["output"]); err != nil {
				return err
			}
		}
		if status, ok := decodeJSONString(envelope["status"]); ok {
			if status == "incomplete" {
				assembly.terminal = status
				assembly.incompleteReason = incompleteDescription(envelope["incomplete_details"])
			} else if status == "failed" {
				assembly.terminal = status
				assembly.failure = errorDescription(envelope["error"])
			}
		}
		return errStopSSE
	case "response.output_text.delta", "response.refusal.delta":
		text, ok := decodeJSONString(event["delta"])
		if ok && text != "" {
			assembly.text.WriteString(text)
			assembly.sawTextDelta = true
			if err := onProgress(assembly.text.String(), nil); err != nil {
				return err
			}
		}
	case "response.output_text.done", "response.refusal.done":
		if !assembly.sawTextDelta {
			if text, ok := decodeJSONString(event["text"]); ok {
				assembly.text.WriteString(text)
			}
		}
	case "response.reasoning_text.delta":
		if delta, ok := decodeJSONString(event["delta"]); ok && delta != "" {
			item := assembly.appendThinking(responseThinkingKey(event, "reasoning"), "reasoning", delta)
			if err := onProgress("", []contracts.ThinkingItem{item}); err != nil {
				return err
			}
		}
	case "response.reasoning_text.done":
		if text, ok := decodeJSONString(event["text"]); ok {
			if item, changed := assembly.setThinkingText(responseThinkingKey(event, "reasoning"), "reasoning", text); changed {
				if err := onProgress("", []contracts.ThinkingItem{item}); err != nil {
					return err
				}
			}
		}
	case "response.reasoning_summary_text.delta":
		if delta, ok := decodeJSONString(event["delta"]); ok && delta != "" {
			item := assembly.appendThinking(responseThinkingKey(event, "summary"), "summary", delta)
			if err := onProgress("", []contracts.ThinkingItem{item}); err != nil {
				return err
			}
		}
	case "response.reasoning_summary_text.done":
		if text, ok := decodeJSONString(event["text"]); ok {
			if item, changed := assembly.setThinkingText(responseThinkingKey(event, "summary"), "summary", text); changed {
				if err := onProgress("", []contracts.ThinkingItem{item}); err != nil {
					return err
				}
			}
		}
	case "response.function_call_arguments.delta":
		assembly.addResponsesCallDelta(event)
	case "response.function_call_arguments.done":
		assembly.addResponsesCallDone(event)
	case "response.output_item.added", "response.output_item.done":
		index, _ := decodeJSONInt(event["output_index"])
		var item json.RawMessage
		if json.Unmarshal(event["item"], &item) == nil && len(item) > 0 {
			for _, thinking := range assembly.addResponsesItem(index, item) {
				if err := onProgress("", []contracts.ThinkingItem{thinking}); err != nil {
					return err
				}
			}
			if typeName == "response.output_item.done" {
				assembly.outputItems[index] = append(json.RawMessage(nil), item...)
			}
		}
	}
	return nil
}

func (assembly *streamAssembly) addResponsesCallDelta(event map[string]json.RawMessage) {
	index, ok := decodeJSONInt(event["output_index"])
	if !ok {
		return
	}
	call := assembly.responsesCall(index)
	if delta, ok := decodeJSONString(event["delta"]); ok {
		call.arguments.WriteString(delta)
	}
	assembly.setResponsesCallID(event, call)
}

func (assembly *streamAssembly) addResponsesCallDone(event map[string]json.RawMessage) {
	index, ok := decodeJSONInt(event["output_index"])
	if !ok {
		return
	}
	call := assembly.responsesCall(index)
	if args, ok := decodeJSONString(event["arguments"]); ok {
		call.finalArgs = args
	}
	assembly.setResponsesCallID(event, call)
	var item map[string]json.RawMessage
	if json.Unmarshal(event["item"], &item) == nil {
		if id, ok := decodeJSONString(item["call_id"]); ok && id != "" {
			call.callID = id
		}
		if name, ok := decodeJSONString(item["name"]); ok && name != "" {
			call.name = name
		}
		if args, ok := decodeJSONString(item["arguments"]); ok {
			call.finalArgs = args
		}
	}
}

func (assembly *streamAssembly) setResponsesCallID(event map[string]json.RawMessage, call *toolCallAssembly) {
	var item map[string]json.RawMessage
	if json.Unmarshal(event["item"], &item) == nil {
		if id, ok := decodeJSONString(item["call_id"]); ok && id != "" {
			call.callID = id
		}
		if name, ok := decodeJSONString(item["name"]); ok && name != "" {
			call.name = name
		}
	}
}

func (assembly *streamAssembly) responsesCall(index int) *toolCallAssembly {
	call := assembly.toolCalls[index]
	if call == nil {
		call = &toolCallAssembly{}
		assembly.toolCalls[index] = call
	}
	return call
}

func (assembly *streamAssembly) addResponsesItem(index int, raw json.RawMessage) []contracts.ThinkingItem {
	var item map[string]json.RawMessage
	if json.Unmarshal(raw, &item) != nil {
		return nil
	}
	typeName, _ := decodeJSONString(item["type"])
	switch typeName {
	case "function_call":
		call := assembly.responsesCall(index)
		if id, ok := decodeJSONString(item["call_id"]); ok && id != "" {
			call.callID = id
		}
		if name, ok := decodeJSONString(item["name"]); ok && name != "" {
			call.name = name
		}
		if args, ok := decodeJSONString(item["arguments"]); ok {
			call.finalArgs = args
		}
	case "message":
		if !assembly.sawTextDelta {
			assembly.appendOutputMessageText(item["content"])
		}
	case "reasoning":
		itemID, _ := decodeJSONString(item["id"])
		changed := make([]contracts.ThinkingItem, 0)
		var content []json.RawMessage
		if json.Unmarshal(item["content"], &content) == nil {
			for contentIndex, rawContent := range content {
				var part map[string]json.RawMessage
				if json.Unmarshal(rawContent, &part) != nil {
					continue
				}
				partType, _ := decodeJSONString(part["type"])
				text, ok := decodeJSONString(part["text"])
				if !ok || text == "" || partType != "reasoning_text" {
					continue
				}
				key := responseItemThinkingKey(itemID, index, "reasoning", contentIndex)
				if current, didChange := assembly.setThinkingText(key, "reasoning", text); didChange {
					changed = append(changed, current)
				}
			}
		}
		var summary []json.RawMessage
		if json.Unmarshal(item["summary"], &summary) == nil {
			for summaryIndex, rawSummary := range summary {
				var part map[string]json.RawMessage
				if json.Unmarshal(rawSummary, &part) != nil {
					continue
				}
				partType, _ := decodeJSONString(part["type"])
				text, ok := decodeJSONString(part["text"])
				if !ok || text == "" || (partType != "summary_text" && partType != "") {
					continue
				}
				key := responseItemThinkingKey(itemID, index, "summary", summaryIndex)
				if current, didChange := assembly.setThinkingText(key, "summary", text); didChange {
					changed = append(changed, current)
				}
			}
		}
		return changed
	}
	return nil
}

func (assembly *streamAssembly) captureResponsesOutput(raw json.RawMessage) error {
	var output []json.RawMessage
	if err := json.Unmarshal(raw, &output); err != nil {
		return fmt.Errorf("decode Responses output: %w", err)
	}
	assembly.replayItems = make([]json.RawMessage, 0, len(output))
	for index, item := range output {
		copyItem := append(json.RawMessage(nil), item...)
		assembly.replayItems = append(assembly.replayItems, copyItem)
		assembly.outputItems[index] = copyItem
		assembly.addResponsesItem(index, item)
	}
	if !assembly.sawTextDelta && assembly.text.Len() == 0 {
		for _, item := range output {
			var decoded map[string]json.RawMessage
			if json.Unmarshal(item, &decoded) == nil {
				if typeName, _ := decodeJSONString(decoded["type"]); typeName == "message" {
					assembly.appendOutputMessageText(decoded["content"])
				}
			}
		}
	}
	return nil
}

func (assembly *streamAssembly) captureCompletedOutputItems() {
	indices := make([]int, 0, len(assembly.outputItems))
	for index := range assembly.outputItems {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	assembly.replayItems = make([]json.RawMessage, 0, len(indices))
	for _, index := range indices {
		assembly.replayItems = append(assembly.replayItems, append(json.RawMessage(nil), assembly.outputItems[index]...))
	}
}

func (assembly *streamAssembly) appendOutputMessageText(raw json.RawMessage) {
	var content []json.RawMessage
	if json.Unmarshal(raw, &content) != nil {
		return
	}
	for _, part := range content {
		var value map[string]json.RawMessage
		if json.Unmarshal(part, &value) != nil {
			continue
		}
		typeName, _ := decodeJSONString(value["type"])
		field := "text"
		if typeName == "refusal" {
			field = "refusal"
		} else if typeName != "output_text" {
			continue
		}
		if text, ok := decodeJSONString(value[field]); ok {
			assembly.text.WriteString(text)
		}
	}
}

func (assembly *streamAssembly) result(provider *Provider, request contracts.ModelRequest) (contracts.ModelResult, error) {
	if assembly.terminal == "failed" {
		message := assembly.failure
		if message == "" {
			message = "provider stream failed"
		}
		return contracts.ModelResult{}, errors.New(message)
	}
	if assembly.terminal == "incomplete" {
		message := "provider response incomplete"
		if assembly.incompleteReason != "" {
			message += ": " + assembly.incompleteReason
		}
		return contracts.ModelResult{}, errors.New(message)
	}
	if assembly.api == "responses" {
		if !assembly.responseDone || assembly.terminal != "completed" {
			return contracts.ModelResult{}, errors.New("Responses stream ended before response.completed")
		}
	} else if !assembly.chatDone {
		return contracts.ModelResult{}, errors.New("Chat Completions stream ended before [DONE]")
	}
	calls, err := assembly.toolCallResults()
	if err != nil {
		return contracts.ModelResult{}, err
	}
	model := assembly.responseModel
	if model == "" {
		model = request.Model.ModelID
	}
	state := assembly.providerState(provider, model)
	finishReason := assembly.finishReason
	if assembly.api == "responses" {
		finishReason = "completed"
	}
	return contracts.ModelResult{
		Text:          assembly.text.String(),
		FinishReason:  finishReason,
		Calls:         calls,
		ProviderState: state,
		Usage:         append(json.RawMessage(nil), assembly.usage...),
		Thinking:      assembly.thinkingItems(),
	}, nil
}

func responseThinkingKey(event map[string]json.RawMessage, kind string) string {
	itemID, _ := decodeJSONString(event["item_id"])
	outputIndex, _ := decodeJSONInt(event["output_index"])
	indexName := "content_index"
	if kind == "summary" {
		indexName = "summary_index"
	}
	partIndex, hasPartIndex := decodeJSONInt(event[indexName])
	key := itemID
	if key == "" {
		key = fmt.Sprintf("output/%d", outputIndex)
	}
	if !hasPartIndex {
		partIndex = 0
	}
	return responseItemThinkingKey(key, outputIndex, kind, partIndex)
}

func responseItemThinkingKey(itemID string, outputIndex int, kind string, partIndex int) string {
	if itemID == "" {
		itemID = fmt.Sprintf("output/%d", outputIndex)
	}
	return fmt.Sprintf("%s/%s/%d", itemID, kind, partIndex)
}

func (assembly *streamAssembly) appendThinking(key, kind, delta string) contracts.ThinkingItem {
	text := assembly.thinking[key]
	if text == nil {
		text = &strings.Builder{}
		assembly.thinking[key] = text
		assembly.thinkingOrder = append(assembly.thinkingOrder, key)
	}
	assembly.thinkingKind[key] = kind
	text.WriteString(delta)
	return contracts.ThinkingItem{Key: key, Text: text.String(), Kind: kind}
}

func (assembly *streamAssembly) setThinkingText(key, kind, value string) (contracts.ThinkingItem, bool) {
	text := assembly.thinking[key]
	if text == nil {
		text = &strings.Builder{}
		assembly.thinking[key] = text
		assembly.thinkingOrder = append(assembly.thinkingOrder, key)
	}
	assembly.thinkingKind[key] = kind
	if text.String() == value {
		return contracts.ThinkingItem{Key: key, Text: value, Kind: kind}, false
	}
	text.Reset()
	text.WriteString(value)
	return contracts.ThinkingItem{Key: key, Text: value, Kind: kind}, true
}

func (assembly *streamAssembly) thinkingItems() []contracts.ThinkingItem {
	items := make([]contracts.ThinkingItem, 0, len(assembly.thinkingOrder))
	for _, key := range assembly.thinkingOrder {
		if text := assembly.thinking[key]; text != nil && text.Len() > 0 {
			items = append(items, contracts.ThinkingItem{Key: key, Text: text.String(), Kind: assembly.thinkingKind[key]})
		}
	}
	return items
}

func (assembly *streamAssembly) toolCallResults() ([]contracts.ToolCall, error) {
	indices := make([]int, 0, len(assembly.toolCalls))
	for index := range assembly.toolCalls {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	calls := make([]contracts.ToolCall, 0, len(indices))
	for _, index := range indices {
		call := assembly.toolCalls[index]
		arguments := call.finalArgs
		if arguments == "" {
			arguments = call.arguments.String()
		}
		if call.callID == "" || call.name == "" || !json.Valid([]byte(arguments)) {
			return nil, fmt.Errorf("provider returned an incomplete tool call at index %d", index)
		}
		calls = append(calls, contracts.ToolCall{
			Kind:      "tool_call",
			CallID:    call.callID,
			Name:      call.name,
			Arguments: append(json.RawMessage(nil), []byte(arguments)...),
		})
	}
	return calls, nil
}

func (assembly *streamAssembly) providerState(provider *Provider, model string) *contracts.ProviderState {
	state := &contracts.ProviderState{Provider: provider.config.StateIdentity, Model: model}
	if assembly.api == "responses" && len(assembly.replayItems) > 0 {
		state.ReplayItems = cloneRawMessages(assembly.replayItems)
		return state
	}
	if assembly.api == "chat-completions" && provider.config.Options.ReasoningReplay && provider.config.Options.ReasoningField != "" {
		if len(assembly.chatReasoningRaw) > 0 {
			state.ReasoningDetails = cloneRawMessages(assembly.chatReasoningRaw)
			return state
		}
		if assembly.chatReasoning.Len() > 0 {
			state.Reasoning = &contracts.Reasoning{Field: provider.config.Options.ReasoningField, Text: assembly.chatReasoning.String()}
			return state
		}
	}
	if len(state.ReplayItems) == 0 && len(state.ReasoningDetails) == 0 && state.Reasoning == nil {
		return nil
	}
	return state
}

func errorDescription(raw json.RawMessage) string {
	if !presentJSON(raw) {
		return ""
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) == nil {
		code, _ := decodeJSONString(value["code"])
		message, _ := decodeJSONString(value["message"])
		if code != "" && message != "" {
			return code + ": " + message
		}
		if message != "" {
			return message
		}
		if code != "" {
			return code
		}
	}
	return string(raw)
}

func incompleteDescription(raw json.RawMessage) string {
	if !presentJSON(raw) {
		return ""
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) == nil {
		if reason, ok := decodeJSONString(value["reason"]); ok {
			return reason
		}
	}
	return string(raw)
}

func decodeJSONString(raw json.RawMessage) (string, bool) {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func decodeJSONInt(raw json.RawMessage) (int, bool) {
	var value int
	if json.Unmarshal(raw, &value) != nil {
		return 0, false
	}
	return value, true
}

func decodeContent(raw json.RawMessage) string {
	if text, ok := decodeJSONString(raw); ok {
		return text
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var text strings.Builder
	for _, part := range parts {
		if part.Type == "text" || part.Type == "output_text" || part.Type == "" {
			text.WriteString(part.Text)
		}
	}
	return text.String()
}

func presentJSON(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func cloneRawMessages(items []json.RawMessage) []json.RawMessage {
	cloned := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		cloned = append(cloned, append(json.RawMessage(nil), item...))
	}
	return cloned
}
