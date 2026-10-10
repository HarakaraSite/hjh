package contracts

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestOldHenjiSerializerContracts(t *testing.T) {
	b, err := os.ReadFile("testdata/henji-contracts.json")
	if err != nil {
		t.Fatal(err)
	}
	var examples struct {
		Messages  []string `json:"messages"`
		Canonical []struct {
			Input    string `json:"input"`
			Expected string `json:"expected"`
		} `json:"canonical"`
	}
	if err = json.Unmarshal(b, &examples); err != nil {
		t.Fatal(err)
	}
	for _, raw := range examples.Messages {
		var m Message
		if err = json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatal(err)
		}
		encoded, err := EncodeJSON(m)
		if err != nil {
			t.Fatal(err)
		}
		var original, roundtrip any
		if err = json.Unmarshal([]byte(raw), &original); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(encoded, &roundtrip); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(original, roundtrip) {
			t.Fatalf("saved message changed: %s", encoded)
		}
	}
	for _, example := range examples.Canonical {
		b, err := EncodeCanonicalJSON([]byte(example.Input))
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != example.Expected {
			t.Fatalf("old canonical bytes differ: got %q want %q", b, example.Expected)
		}
	}
}
