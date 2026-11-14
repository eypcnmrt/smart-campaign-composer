package segment

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// vocabDoc is the vocabulary as the web app needs to know it: which fields, operators, events, cities and languages exist.
// testdata/vocab.json is that list written down; the web app's tests (web/src/domain/ruleText.test.ts) check that every
// entry has a Turkish wording, so a field or an operator added here cannot reach the screen as a raw English word.
type vocabDoc struct {
	Fields    map[string]vocabField `json:"fields"`
	Events    []string              `json:"events"`
	EventOps  []string              `json:"event_ops"`
	Cities    []string              `json:"cities"`
	Languages []string              `json:"languages"`
	Channels  []string              `json:"channels"`
}

type vocabField struct {
	Kind string   `json:"kind"`
	Ops  []string `json:"ops"`
	Enum []string `json:"enum,omitempty"`
}

func currentVocab() vocabDoc {
	kinds := map[kind]string{kindEnum: "enum", kindInt: "int", kindMoney: "money"}
	doc := vocabDoc{
		Fields:    map[string]vocabField{},
		Events:    eventTypes,
		EventOps:  eventOps,
		Cities:    Cities(),
		Languages: Languages(),
		Channels:  Channels(),
	}
	for name, spec := range defaultFields {
		doc.Fields[name] = vocabField{Kind: kinds[spec.kind], Ops: spec.ops, Enum: spec.enum}
	}
	return doc
}

// The golden file fails this test when the vocabulary changes without it: update testdata/vocab.json (the message shows the
// current content) and add the Turkish wording in web/src/domain/ruleText.ts.
func TestVocab_goldenFile(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/vocab.json")
	if err != nil {
		t.Fatal(err)
	}
	var want vocabDoc
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	got := currentVocab()
	if !reflect.DeepEqual(got, want) {
		pretty, _ := json.MarshalIndent(got, "", "  ")
		t.Fatalf("testdata/vocab.json is out of date. Replace it with this and add the Turkish wording to web/src/domain/ruleText.ts:\n%s", pretty)
	}
}
