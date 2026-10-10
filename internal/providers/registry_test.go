package providers

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"hjh/internal/contracts"
)

func TestResolveUsesConnectionDefaultsAndTreatsModelsAsCandidates(t *testing.T) {
	registry, err := Parse([]byte(`{
		"providers": {
			"openrouter-responses": {
				"api": "responses",
				"auth": {"kind": "api-key", "profile": "openrouter-key"},
				"defaultModel": "vendor/default",
				"defaultEffort": "auto",
				"models": [{"id": "listed-model", "name": "Listed"}]
			},
			"opencode-go-chat": {
				"api": "chat-completions",
				"auth": {"kind": "api-key", "profile": "opencode-key"},
				"defaultModel": "deepseek-v4.1-flash",
				"defaultEffort": "low"
			}
		}
	}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	base := contracts.ModelSelection{
		Provider:    "openrouter-responses",
		API:         "responses",
		AuthProfile: "openrouter-key",
		ModelID:     "listed-model",
		Effort:      "auto",
	}

	got, err := registry.Resolve(base, contracts.ModelSelection{ModelID: "explicit-model-outside-catalog"})
	if err != nil {
		t.Fatalf("Resolve(model-only) error = %v", err)
	}
	want := contracts.ModelSelection{
		Provider:    "openrouter-responses",
		API:         "responses",
		AuthProfile: "openrouter-key",
		ModelID:     "explicit-model-outside-catalog",
		Effort:      "auto",
	}
	if got != want {
		t.Fatalf("Resolve(model-only) = %+v, want %+v", got, want)
	}

	got, err = registry.Resolve(base, contracts.ModelSelection{Provider: "opencode-go-chat", API: "ignored-api"})
	if err != nil {
		t.Fatalf("Resolve(provider-only) error = %v", err)
	}
	want = contracts.ModelSelection{
		Provider:    "opencode-go-chat",
		API:         "chat-completions",
		AuthProfile: "opencode-key",
		ModelID:     "deepseek-v4.1-flash",
		Effort:      "low",
	}
	if got != want {
		t.Fatalf("Resolve(provider-only) = %+v, want %+v", got, want)
	}
}

func TestListAndDefinitionExposeProviderIDsFromMapKeys(t *testing.T) {
	registry, err := Parse([]byte(`{
		"providers": {
			"second": {"id": "input-id-is-ignored", "displayName": "Second"},
			"first": {"displayName": "First"}
		}
	}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	definitions := registry.List()
	if len(definitions) != 2 || definitions[0].ID != "first" || definitions[1].ID != "second" {
		t.Fatalf("List() IDs = %+v, want [first second]", definitions)
	}
	encoded, err := json.Marshal(definitions)
	if err != nil {
		t.Fatalf("marshal List() output: %v", err)
	}
	var listed []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(encoded, &listed); err != nil {
		t.Fatalf("unmarshal List() output: %v", err)
	}
	if len(listed) != 2 || listed[0].ID != "first" || listed[1].ID != "second" {
		t.Fatalf("serialized List() IDs = %+v, want [first second]", listed)
	}

	definition, err := registry.Definition("second")
	if err != nil {
		t.Fatalf("Definition() error = %v", err)
	}
	if definition.ID != "second" || definition.DisplayName != "Second" {
		t.Fatalf("Definition() = %+v, want ID second and display name Second", definition)
	}
}

func TestStandardConfigUsesExternalProviderDefinitions(t *testing.T) {
	data, err := os.ReadFile("../extensions/assets/config.json")
	if err != nil {
		t.Fatalf("read standard config: %v", err)
	}
	registry, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse(standard config) error = %v", err)
	}

	wantIDs := []string{
		"openrouter-responses",
		"opencode-go-chat",
		"opencode-go-responses",
		"openai-responses",
		"openai-chatgpt",
	}
	for _, id := range wantIDs {
		if _, err := registry.Definition(id); err != nil {
			t.Errorf("standard config is missing provider %q: %v", id, err)
		}
	}
	if got := len(registry.List()); got != len(wantIDs) {
		t.Fatalf("standard provider count = %d, want %d", got, len(wantIDs))
	}

	wantDefault := contracts.ModelSelection{
		Provider:    "openrouter-responses",
		API:         "responses",
		AuthProfile: "openrouter-api-key",
		ModelID:     "deepseek/deepseek-v4.1-flash",
		Effort:      "auto",
	}
	if got := registry.Config.Model; got != wantDefault {
		t.Fatalf("standard config default model = %+v, want %+v", got, wantDefault)
	}

	goResponses, err := registry.Definition("opencode-go-responses")
	if err != nil {
		t.Fatalf("Definition(opencode-go-responses) error = %v", err)
	}
	if !slices.ContainsFunc(goResponses.Models, func(model ModelInfo) bool { return model.ID == "gpt-6-luna" }) {
		t.Fatal("OpenCode Go Responses catalog is missing its documented gpt-6-luna model")
	}

	chatGPT, err := registry.Definition("openai-chatgpt")
	if err != nil {
		t.Fatalf("Definition(openai-chatgpt) error = %v", err)
	}
	if chatGPT.Catalog.Kind != "openai-account" || chatGPT.Catalog.Path != "/models" {
		t.Fatalf("ChatGPT catalog = %+v, want the account model endpoint", chatGPT.Catalog)
	}
}
