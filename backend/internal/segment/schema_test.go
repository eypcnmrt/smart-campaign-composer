package segment

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
)

// schemaDoc converts the schema to plain JSON values, which is what the API
// receives and what the checks below walk.
func schemaDoc(t *testing.T, schema map[string]any) map[string]any {
	t.Helper()
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	return doc
}

// walkSchema calls fn for the node itself, then for every $defs entry,
// property schema, items schema and anyOf member below it.
func walkSchema(path string, node map[string]any, fn func(path string, n map[string]any)) {
	fn(path, node)
	for _, key := range []string{"$defs", "properties"} {
		children, _ := node[key].(map[string]any)
		for _, name := range slices.Sorted(maps.Keys(children)) {
			walkSchema(path+"."+key+"."+name, children[name].(map[string]any), fn)
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		walkSchema(path+".items", items, fn)
	}
	members, _ := node["anyOf"].([]any)
	for i, m := range members {
		walkSchema(fmt.Sprintf("%s.anyOf[%d]", path, i), m.(map[string]any), fn)
	}
}

// strs reads a JSON array of strings.
func strs(t *testing.T, v any) []string {
	t.Helper()
	list, ok := v.([]any)
	if !ok {
		t.Fatalf("want an array, got %T", v)
	}
	out := make([]string, len(list))
	for i, e := range list {
		s, ok := e.(string)
		if !ok {
			t.Fatalf("array element %d: want a string, got %T", i, e)
		}
		out[i] = s
	}
	return out
}

func TestSchema_returnsFreshMap(t *testing.T) {
	t.Parallel()
	first := Schema()
	first["tampered"] = true
	delete(first, "properties")
	second := Schema()
	if _, ok := second["tampered"]; ok {
		t.Error("Schema() shares state between calls")
	}
	if _, ok := second["properties"]; !ok {
		t.Error("Schema() shares state between calls")
	}
}

// Structured outputs accept only a subset of JSON Schema. Anything outside the
// allow-list fails here instead of as a 400 from the API.
var allowedSchemaKeywords = []string{
	"$defs", "$ref", "additionalProperties", "anyOf", "const", "description",
	"enum", "items", "properties", "required", "type",
}

func TestSchema_usesOnlySupportedKeywords(t *testing.T) {
	t.Parallel()
	walkSchema("schema", schemaDoc(t, Schema()), func(path string, n map[string]any) {
		for key := range n {
			if !slices.Contains(allowedSchemaKeywords, key) {
				t.Errorf("%s: keyword %q is not supported by structured outputs", path, key)
			}
		}
		if ap, ok := n["additionalProperties"]; ok && ap != false {
			t.Errorf("%s: additionalProperties must be false, got %v", path, ap)
		}
		if n["type"] == "array" {
			if _, ok := n["items"]; !ok {
				t.Errorf("%s: array without items", path)
			}
		}
		if n["type"] != "object" {
			return
		}
		if n["additionalProperties"] != false {
			t.Errorf("%s: object must set additionalProperties to false", path)
		}
		properties, _ := n["properties"].(map[string]any)
		required := strs(t, n["required"])
		if !slices.Equal(slices.Sorted(maps.Keys(properties)), slices.Sorted(slices.Values(required))) {
			t.Errorf("%s: required %v must list exactly the properties %v", path, required, slices.Sorted(maps.Keys(properties)))
		}
	})
}

// refsIn lists the definitions a schema node refers to, anywhere below it.
func refsIn(t *testing.T, node map[string]any) []string {
	t.Helper()
	var out []string
	walkSchema("", node, func(path string, n map[string]any) {
		r, ok := n["$ref"].(string)
		if !ok {
			return
		}
		name, found := strings.CutPrefix(r, "#/$defs/")
		if !found {
			t.Errorf("%s: $ref %q must point into #/$defs", path, r)
		}
		out = append(out, name)
	})
	return out
}

// refCycle returns a path that leads back to a definition already on it, or
// nil when the graph has no cycle.
func refCycle(graph map[string][]string) []string {
	const (
		unseen = iota
		onPath
		done
	)
	state := map[string]int{}
	var path []string
	var visit func(name string) []string
	visit = func(name string) []string {
		switch state[name] {
		case onPath:
			return append(slices.Clone(path), name)
		case done:
			return nil
		}
		state[name] = onPath
		path = append(path, name)
		for _, next := range graph[name] {
			if cycle := visit(next); cycle != nil {
				return cycle
			}
		}
		path = path[:len(path)-1]
		state[name] = done
		return nil
	}
	for _, name := range slices.Sorted(maps.Keys(graph)) {
		if cycle := visit(name); cycle != nil {
			return cycle
		}
	}
	return nil
}

func TestRefCycle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		graph map[string][]string
		want  bool
	}{
		{"empty", map[string][]string{}, false},
		{"chain", map[string][]string{"a": {"b"}, "b": {"c"}, "c": nil}, false},
		{"diamond", map[string][]string{"a": {"b", "c"}, "b": {"d"}, "c": {"d"}, "d": nil}, false},
		{"self reference", map[string][]string{"a": {"a"}}, true},
		{"two step cycle", map[string][]string{"a": {"b"}, "b": {"a"}}, true},
		{"cycle behind a chain", map[string][]string{"a": {"b"}, "b": {"c"}, "c": {"b"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := refCycle(tt.graph) != nil; got != tt.want {
				t.Errorf("refCycle() found a cycle = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSchema_refsResolveWithoutRecursion(t *testing.T) {
	t.Parallel()
	doc := schemaDoc(t, Schema())
	defs := doc["$defs"].(map[string]any)

	graph := map[string][]string{}
	for name, def := range defs {
		graph[name] = refsIn(t, def.(map[string]any))
	}
	for name, targets := range graph {
		for _, target := range targets {
			if _, ok := defs[target]; !ok {
				t.Errorf("definition %q refers to unknown definition %q", name, target)
			}
		}
	}
	if cycle := refCycle(graph); cycle != nil {
		t.Errorf("recursive definitions are not supported by structured outputs: %s", strings.Join(cycle, " -> "))
	}

	// Every definition must be used, or it is dead weight in every request.
	reached := map[string]bool{}
	var mark func(name string)
	mark = func(name string) {
		if reached[name] {
			return
		}
		reached[name] = true
		for _, next := range graph[name] {
			mark(next)
		}
	}
	rootRefs := map[string]any{"properties": doc["properties"]}
	for _, name := range refsIn(t, rootRefs) {
		mark(name)
	}
	for name := range defs {
		if !reached[name] {
			t.Errorf("definition %q is not reachable from the schema root", name)
		}
	}
}

// any_of must not nest: the group may refer only to single conditions.
func TestSchema_groupsCannotNest(t *testing.T) {
	t.Parallel()
	doc := schemaDoc(t, Schema())
	defs := doc["$defs"].(map[string]any)

	group := defs["group"].(map[string]any)
	items := group["properties"].(map[string]any)["conditions"].(map[string]any)["items"].(map[string]any)
	if items["$ref"] != "#/$defs/condition" {
		t.Errorf("group items must refer to condition, got %v", items)
	}
	for _, target := range refsIn(t, defs["condition"].(map[string]any)) {
		if target == "group" || target == "condition" {
			t.Errorf("condition refers to %q; a group would be able to contain a group", target)
		}
	}
}

// variant is one attribute or event alternative as the schema describes it.
type variant struct {
	field     string
	ops       []string
	values    []string // enum members, empty for numbers
	valueType string   // "string", "integer" or "number"
}

// schemaVariants extracts the attribute variants and the event variant.
func schemaVariants(t *testing.T, doc map[string]any) (attrs []variant, events []variant) {
	t.Helper()
	defs := doc["$defs"].(map[string]any)
	for _, name := range slices.Sorted(maps.Keys(defs)) {
		def := defs[name].(map[string]any)
		properties, _ := def["properties"].(map[string]any)
		typ, _ := properties["type"].(map[string]any)
		switch typ["const"] {
		case typeAttribute:
			v := variant{
				field: properties["field"].(map[string]any)["const"].(string),
				ops:   strs(t, properties["op"].(map[string]any)["enum"]),
			}
			value := properties["value"].(map[string]any)
			if value["type"] == "array" { // in / not_in take a list of the scalar type
				value = value["items"].(map[string]any)
			}
			v.valueType = value["type"].(string)
			if enum, ok := value["enum"]; ok {
				v.values = strs(t, enum)
			}
			attrs = append(attrs, v)
		case typeEvent:
			events = append(events, variant{
				field:     strings.Join(strs(t, properties["event"].(map[string]any)["enum"]), ","),
				ops:       strs(t, properties["op"].(map[string]any)["enum"]),
				valueType: properties["within_days"].(map[string]any)["type"].(string),
			})
		}
	}
	return attrs, events
}

// schemaDrift lists every difference between the schema and the vocabulary,
// in both directions: vocabulary entries missing from the schema and schema
// entries missing from the vocabulary.
func schemaDrift(t *testing.T, doc map[string]any, fs fieldSet, evTypes, evOps, chans []string) []string {
	t.Helper()
	var drift []string
	report := func(format string, args ...any) { drift = append(drift, fmt.Sprintf(format, args...)) }

	// diff reports elements present on one side only.
	diff := func(what string, schema, vocab []string) {
		for _, s := range slices.Sorted(slices.Values(schema)) {
			if !slices.Contains(vocab, s) {
				report("%s: %q is in the schema but not in the vocabulary", what, s)
			}
		}
		for _, v := range slices.Sorted(slices.Values(vocab)) {
			if !slices.Contains(schema, v) {
				report("%s: %q is in the vocabulary but not in the schema", what, v)
			}
		}
	}

	attrs, events := schemaVariants(t, doc)

	// the channels a brief can name: the items of the top-level "channels" array
	channelsProp, ok := doc["properties"].(map[string]any)["channels"].(map[string]any)
	if !ok || channelsProp["type"] != "array" {
		report("channels: the schema has no array property \"channels\"")
	} else {
		diff("channels", strs(t, channelsProp["items"].(map[string]any)["enum"]), chans)
	}

	schemaOps := map[string][]string{}
	schemaValues := map[string][]string{}
	schemaTypes := map[string][]string{}
	for _, v := range attrs {
		schemaOps[v.field] = append(schemaOps[v.field], v.ops...)
		schemaValues[v.field] = append(schemaValues[v.field], v.values...)
		if !slices.Contains(schemaTypes[v.field], v.valueType) {
			schemaTypes[v.field] = append(schemaTypes[v.field], v.valueType)
		}
	}
	diff("fields", slices.Collect(maps.Keys(schemaOps)), fs.names())

	for _, field := range fs.names() {
		spec := fs[field]
		diff("operators of "+field, schemaOps[field], spec.ops)

		wantType := map[kind]string{kindEnum: "string", kindInt: "integer", kindMoney: "number"}[spec.kind]
		diff("value type of "+field, schemaTypes[field], []string{wantType})
		if spec.kind == kindEnum {
			diff("values of "+field, schemaValues[field], spec.enum)
		} else if len(schemaValues[field]) > 0 {
			report("values of %s: the schema lists an enum for a numeric field", field)
		}
	}

	if len(events) != 1 {
		report("events: want exactly one event variant in the schema, got %d", len(events))
		return drift
	}
	diff("event types", strings.Split(events[0].field, ","), evTypes)
	diff("event operators", events[0].ops, evOps)
	if events[0].valueType != "integer" {
		report("within_days: want type integer, got %s", events[0].valueType)
	}
	return drift
}

func TestSchema_matchesVocabulary(t *testing.T) {
	t.Parallel()
	for _, d := range schemaDrift(t, schemaDoc(t, Schema()), defaultFields, eventTypes, eventOps, channelNames) {
		t.Error(d)
	}
}

// cloneFields copies a fieldSet deeply enough to edit it in a test.
func cloneFields(fs fieldSet) fieldSet {
	out := fieldSet{}
	for name, spec := range fs {
		spec.ops = slices.Clone(spec.ops)
		spec.enum = slices.Clone(spec.enum)
		out[name] = spec
	}
	return out
}

// The comparison above is only worth having if it notices changes, so edit the
// vocabulary and require a report that names the change.
func TestSchemaDrift_detectsVocabularyChanges(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		edit    func(fs fieldSet) fieldSet
		evTypes []string
		evOps   []string
		chans   []string // nil: the real channel list
		want    string
	}{
		{
			name:  "channel added to the vocabulary",
			edit:  func(fs fieldSet) fieldSet { return fs },
			chans: []string{"sms", "email", "push"},
			want:  `"push" is in the vocabulary but not in the schema`,
		},
		{
			name:  "channel removed from the vocabulary",
			edit:  func(fs fieldSet) fieldSet { return fs },
			chans: []string{"sms"},
			want:  `"email" is in the schema but not in the vocabulary`,
		},
		{
			name: "field added to the vocabulary",
			edit: func(fs fieldSet) fieldSet {
				fs["last_login_days"] = fieldSpec{column: "u.x", kind: kindInt, ops: numericOps}
				return fs
			},
			want: `"last_login_days" is in the vocabulary but not in the schema`,
		},
		{
			name: "field removed from the vocabulary",
			edit: func(fs fieldSet) fieldSet { delete(fs, "language"); return fs },
			want: `"language" is in the schema but not in the vocabulary`,
		},
		{
			name: "operator added to the vocabulary",
			edit: func(fs fieldSet) fieldSet {
				s := fs["total_orders"]
				s.ops = append(s.ops, "between")
				fs["total_orders"] = s
				return fs
			},
			want: `"between" is in the vocabulary but not in the schema`,
		},
		{
			name: "operator removed from the vocabulary",
			edit: func(fs fieldSet) fieldSet {
				s := fs["city"]
				s.ops = slices.DeleteFunc(s.ops, func(op string) bool { return op == opNotIn })
				fs["city"] = s
				return fs
			},
			want: `"not_in" is in the schema but not in the vocabulary`,
		},
		{
			name: "enum value added to the vocabulary",
			edit: func(fs fieldSet) fieldSet {
				s := fs["city"]
				s.enum = append(s.enum, "Mersin")
				fs["city"] = s
				return fs
			},
			want: `"Mersin" is in the vocabulary but not in the schema`,
		},
		{
			name: "enum value removed from the vocabulary",
			edit: func(fs fieldSet) fieldSet {
				s := fs["language"]
				s.enum = []string{"tr"}
				fs["language"] = s
				return fs
			},
			want: `"en" is in the schema but not in the vocabulary`,
		},
		{
			name: "field changes kind",
			edit: func(fs fieldSet) fieldSet {
				s := fs["total_orders"]
				s.kind = kindMoney
				fs["total_orders"] = s
				return fs
			},
			want: "value type of total_orders",
		},
		{
			name:    "event type added",
			edit:    func(fs fieldSet) fieldSet { return fs },
			evTypes: []string{"cart_add", "purchase", "app_open"},
			evOps:   eventOps,
			want:    `"app_open" is in the vocabulary but not in the schema`,
		},
		{
			name:    "event operator removed",
			edit:    func(fs fieldSet) fieldSet { return fs },
			evTypes: eventTypes,
			evOps:   []string{opPerformed},
			want:    `"not_performed" is in the schema but not in the vocabulary`,
		},
	}
	doc := schemaDoc(t, Schema())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			evTypes, evOps := tt.evTypes, tt.evOps
			if evTypes == nil {
				evTypes, evOps = eventTypes, eventOps
			}
			chans := tt.chans
			if chans == nil {
				chans = channelNames
			}
			drift := schemaDrift(t, doc, tt.edit(cloneFields(defaultFields)), evTypes, evOps, chans)
			if !slices.ContainsFunc(drift, func(d string) bool { return strings.Contains(d, tt.want) }) {
				t.Errorf("drift = %q\nwant a report containing %s", drift, tt.want)
			}
		})
	}
}

// examples returns example values for a schema node. Enums contribute every
// member, so the examples cover the whole vocabulary.
func examples(t *testing.T, defs, node map[string]any) []any {
	t.Helper()
	if r, ok := node["$ref"].(string); ok {
		return examples(t, defs, defs[strings.TrimPrefix(r, "#/$defs/")].(map[string]any))
	}
	if c, ok := node["const"]; ok {
		return []any{c}
	}
	if enum, ok := node["enum"].([]any); ok {
		return enum
	}
	if members, ok := node["anyOf"].([]any); ok {
		var out []any
		for _, m := range members {
			out = append(out, examples(t, defs, m.(map[string]any))...)
		}
		return out
	}
	switch node["type"] {
	case "string":
		return []any{"text"}
	case "integer":
		return []any{1, 100}
	case "number":
		return []any{1.5, 1500.5}
	case "array":
		item := examples(t, defs, node["items"].(map[string]any))
		var out []any
		for _, it := range item {
			out = append(out, []any{it})
		}
		if len(item) >= 2 {
			out = append(out, []any{item[0], item[1]})
		}
		return out
	case "object":
		properties := node["properties"].(map[string]any)
		out := []any{map[string]any{}}
		for _, name := range slices.Sorted(maps.Keys(properties)) {
			var next []any
			for _, base := range out {
				for _, val := range examples(t, defs, properties[name].(map[string]any)) {
					o := maps.Clone(base.(map[string]any))
					o[name] = val
					next = append(next, o)
				}
			}
			out = next
		}
		return out
	}
	t.Fatalf("no examples for schema node %v", node)
	return nil
}

// Everything the schema can express must pass Decode and Validate, otherwise
// the model could be forced into output the repair loop can never fix.
func TestSchema_everyExampleDecodesAndValidates(t *testing.T) {
	t.Parallel()
	doc := schemaDoc(t, Schema())
	defs := doc["$defs"].(map[string]any)
	all := examples(t, defs, doc)
	if len(all) < 100 {
		t.Fatalf("only %d examples generated; the generator is not covering the schema", len(all))
	}

	var groups, events int
	for _, rule := range all {
		data, err := json.Marshal(rule)
		if err != nil {
			t.Fatalf("marshal example: %v", err)
		}
		s := string(data)
		groups += strings.Count(s, `"any_of"`)
		events += strings.Count(s, `"within_days"`)

		r, err := Decode(data)
		if err != nil {
			t.Fatalf("Decode(%s): %v", s, err)
		}
		if err := Validate(r); err != nil {
			t.Fatalf("Validate(%s): %v", s, err)
		}
	}
	if groups == 0 || events == 0 {
		t.Errorf("examples include %d groups and %d events; want both", groups, events)
	}
}
