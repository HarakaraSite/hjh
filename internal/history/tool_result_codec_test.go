package history

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"hjh/internal/contracts"
)

func TestToolObservationResultBodyIsOwnedAndExpandedOnRead(t *testing.T) {
	ctx := context.Background()
	store, sessionID := recallTestStore(t)
	execution := beginRecallTestExecution(t, store, sessionID, "result-codec", "inspect tool results")
	largeString := []byte(`"` + strings.Repeat("file output ", 2048) + `"`)
	cases := []struct {
		name           string
		kind           string
		payload        json.RawMessage
		observedResult []byte
		result         []byte
	}{
		{name: "read file object", kind: "tool_result",
			payload: json.RawMessage(`{"callId":"read-1","name":"read_file","phase":"completed","outcome":"completed","preview":"output.txt","result": { "text" : "first\n", "future" : [1, true] }}`),
			result:  []byte(`{ "text" : "first\n", "future" : [1, true] }`)},
		{name: "host null", kind: "effect_observation",
			payload: json.RawMessage(`{"callId":"host-null","name":"command","phase":"completed","result":null}`), result: []byte(`null`)},
		{name: "host string", kind: "effect_observation",
			payload: json.RawMessage(`{"callId":"host-string","name":"command","phase":"completed","result":"command output\\n"}`), result: []byte(`"command output\\n"`)},
		{name: "tool number", kind: "tool_result",
			payload: json.RawMessage(`{"callId":"number","name":"custom","phase":"completed","result":1.25e+2}`), result: []byte(`1.25e+2`)},
		{name: "large output string", kind: "tool_result",
			payload: json.RawMessage(append(append([]byte(`{"callId":"large","name":"read_file","phase":"completed","outcome":"completed","preview":"output.txt","result":`), largeString...), '}')),
			result:  largeString},
		{name: "original observed result", kind: "tool_result",
			payload:        json.RawMessage(`{"callId":"html","name":"run_js","phase":"completed","result":{"text":"\u003cp\u003e\u0026"}}`),
			observedResult: []byte(`{"text":"<p>&"}`),
			result:         []byte(`{"text":"<p>&"}`)},
		{name: "original host observed result", kind: "effect_observation",
			payload:        json.RawMessage(`{"callId":"host-html","name":"command","phase":"completed","result":"\u003cp\u003e\u0026"}`),
			observedResult: []byte(`"<p>&"`),
			result:         []byte(`"<p>&"`)},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			record, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{
				Kind: test.kind, Payload: test.payload, ObservedResult: test.observedResult,
			})
			if err != nil {
				t.Fatal(err)
			}
			if record.Content != nil {
				t.Fatalf("public AppendOccurrence duplicated result in Content: %d bytes", len(record.Content))
			}
			assertExpandedResult(t, record, test.result)

			var storedPayload string
			var digest sql.NullString
			var body []byte
			if err := store.db.QueryRowContext(ctx, `SELECT r.payload_json,r.content_digest,c.content_bytes
				FROM semantic_records r JOIN contents c ON c.content_digest=r.content_digest WHERE r.record_id=?`, record.ID,
			).Scan(&storedPayload, &digest, &body); err != nil {
				t.Fatal(err)
			}
			if !digest.Valid || digest.String != contentDigest(test.result) || !bytes.Equal(body, test.result) {
				t.Fatalf("stored result bytes/digest differ: digest=%+v body=%q want=%q", digest, body, test.result)
			}
			var stored map[string]json.RawMessage
			if err := json.Unmarshal([]byte(storedPayload), &stored); err != nil {
				t.Fatal(err)
			}
			var resultRef string
			if err := json.Unmarshal(stored["resultRef"], &resultRef); err != nil || resultRef != digest.String {
				t.Fatalf("stored resultRef=%s, content digest=%s, err=%v", stored["resultRef"], digest.String, err)
			}
			if _, present := stored["result"]; present {
				t.Fatal("stored occurrence payload duplicated the result body")
			}
			if test.name == "large output string" && len(storedPayload) >= len(test.result) {
				t.Fatalf("large result was not removed from payload: payload=%d result=%d", len(storedPayload), len(test.result))
			}

			read, err := store.Records(ctx, execution.ID, record.Ordinal, record.Ordinal)
			if err != nil || len(read) != 1 {
				t.Fatalf("Records read = %+v, %v", read, err)
			}
			if read[0].Content != nil {
				t.Fatalf("public Records duplicated result in Content: %d bytes", len(read[0].Content))
			}
			if !bytes.Equal(read[0].Payload, record.Payload) {
				t.Fatalf("AppendOccurrence and Records public payloads differ:\n append: %s\n read:   %s", record.Payload, read[0].Payload)
			}
			assertExpandedResult(t, read[0], test.result)
		})
	}
}

func assertExpandedResult(t *testing.T, record Record, rawResult []byte) {
	t.Helper()
	var public map[string]json.RawMessage
	if err := json.Unmarshal(record.Payload, &public); err != nil {
		t.Fatal(err)
	}
	got, ok := public["result"]
	if !ok {
		t.Fatalf("public payload omitted result: %s", record.Payload)
	}
	want, err := contracts.EncodeCanonicalJSON(rawResult)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("public result = %s, want canonical %s", got, want)
	}
	if _, present := public["resultRef"]; present {
		t.Fatalf("public payload exposed internal resultRef: %s", record.Payload)
	}
	var metadata struct {
		CallID  string `json:"callId"`
		Name    string `json:"name"`
		Phase   string `json:"phase"`
		Outcome string `json:"outcome"`
		Preview string `json:"preview"`
	}
	if err := json.Unmarshal(record.Payload, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.CallID == "" || metadata.Name == "" || metadata.Phase != "completed" {
		t.Fatalf("result metadata was lost: %s", record.Payload)
	}
	if metadata.Name == "read_file" && (metadata.Outcome != "completed" || metadata.Preview != "output.txt") {
		t.Fatalf("tool display metadata was lost: %s", record.Payload)
	}
	if len(record.Payload) > len(rawResult)+512 && len(rawResult) > 16*1024 {
		t.Fatalf("large result was duplicated into public payload: payload=%d result=%d", len(record.Payload), len(rawResult))
	}
}
