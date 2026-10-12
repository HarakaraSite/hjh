package app

import (
	"reflect"
	"strings"
	"testing"
)

func TestDecodeModelCatalogPreservesOpenRouterReasoningEfforts(t *testing.T) {
	const response = `{"data":[
		{"id":"deepseek/deepseek-v4.1-flash","name":"DeepSeek V4.1 Flash","reasoning":{"mandatory":false,"default_enabled":true,"supported_efforts":["max","high","low"],"default_effort":"high"}},
		{"id":"efforts-omitted","name":"Efforts omitted","reasoning":{"default_enabled":true}},
		{"id":"supported-efforts-omitted","name":"Supported efforts omitted","reasoning":{"mandatory":false}},
		{"id":"efforts-null","name":"Efforts null","reasoning":{"mandatory":false,"supported_efforts":null}},
		{"id":"efforts-null-mandatory","name":"Efforts null mandatory","reasoning":{"mandatory":true,"supported_efforts":null}}
	]}`

	models, err := decodeModelCatalog(strings.NewReader(response), "openai")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 5 {
		t.Fatalf("decoded %d models, want 5: %+v", len(models), models)
	}

	deepseek := models[0]
	if deepseek.ID != "deepseek/deepseek-v4.1-flash" || deepseek.Name != "DeepSeek V4.1 Flash" ||
		deepseek.EffortSource != "catalog" || !reflect.DeepEqual(deepseek.Efforts, []string{"max", "high", "low"}) {
		t.Fatalf("DeepSeek reasoning metadata = %+v", deepseek)
	}

	for _, index := range []int{1, 2} {
		if models[index].Efforts != nil || models[index].EffortSource != "" {
			t.Errorf("model %q with omitted supported_efforts exposed a selector: %+v", models[index].ID, models[index])
		}
	}
	if want := []string{"max", "xhigh", "high", "medium", "low", "minimal", "none"}; models[3].EffortSource != "catalog" || !reflect.DeepEqual(models[3].Efforts, want) {
		t.Errorf("explicit null supported_efforts = %+v, want catalog gateway values %v", models[3], want)
	}
	if want := []string{"max", "xhigh", "high", "medium", "low", "minimal"}; models[4].EffortSource != "catalog" || !reflect.DeepEqual(models[4].Efforts, want) {
		t.Errorf("mandatory model efforts = %+v, want gateway values without none %v", models[4], want)
	}

}
