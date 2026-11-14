package segment

import (
	"fmt"
	"slices"
)

// Schema returns the JSON schema of a rule, for structured outputs
// (output_config.format). It is built from the same vocabulary the validator
// uses, so every field, operator and enum value the validator accepts is in
// the schema and nothing else is.
//
// Structured outputs reject recursion and numeric or string constraints, so
// the schema is flat: shared definitions are referenced with $ref but never
// reference themselves, and limits such as within_days 1-365, list lengths and
// the condition count are left to Validate (they appear only in descriptions).
// A fresh map is returned on every call.
func Schema() map[string]any {
	return buildSchema(defaultFields, eventTypes, eventOps)
}

func buildSchema(fs fieldSet, evTypes, evOps []string) map[string]any {
	defs := map[string]any{}
	var conditions []any // $ref of every single-condition variant

	add := func(name string, schema map[string]any) {
		defs[name] = schema
		conditions = append(conditions, ref(name))
	}

	for _, field := range fs.names() {
		spec := fs[field]
		scalarOps, listOps := splitOps(spec.ops)
		if len(scalarOps) > 0 {
			add("attribute_"+field+"_scalar", attributeVariant(field, scalarOps, scalarValue(spec)))
		}
		// Only text fields take lists; the validator rejects them elsewhere.
		if len(listOps) > 0 && spec.kind == kindEnum {
			list := obj{"type": "array", "items": scalarValue(spec)}
			add("attribute_"+field+"_list", attributeVariant(field, listOps, list))
		}
	}

	add("event", object(
		props{
			"type":        constant(typeEvent),
			"event":       enumOf(evTypes),
			"op":          enumOf(evOps),
			"within_days": obj{"type": "integer", "description": fmt.Sprintf("Whole days, 1 to %d", maxWithinDays)},
		}, "type", "event", "op", "within_days"))

	defs["condition"] = obj{"anyOf": conditions}
	defs["group"] = object(
		props{
			"type": constant(typeAnyOf),
			"conditions": obj{
				"type":        "array",
				"items":       ref("condition"),
				"description": fmt.Sprintf("OR group of single conditions, 1 to %d", maxConditions),
			},
		}, "type", "conditions")

	return obj{
		"type": "object",
		"properties": props{
			"conditions": obj{
				"type":        "array",
				"items":       obj{"anyOf": []any{ref("condition"), ref("group")}},
				"description": fmt.Sprintf("Combined with AND, 1 to %d entries", maxConditions),
			},
			"unsupported_criteria": obj{
				"type":        "array",
				"items":       obj{"type": "string"},
				"description": "Parts of the brief the rules cannot express; empty when everything is covered",
			},
			"explanation": obj{"type": "string", "description": "One sentence describing the segment"},
			"channels": obj{
				"type":        "array",
				"items":       enumOf(channelNames),
				"description": "Channels the brief asks for; empty when the brief names none",
			},
		},
		"required":             []any{"conditions", "unsupported_criteria", "explanation", "channels"},
		"additionalProperties": false,
		"$defs":                defs,
	}
}

type (
	obj   = map[string]any
	props = map[string]any
)

func ref(name string) obj { return obj{"$ref": "#/$defs/" + name} }

func constant(v string) obj { return obj{"type": "string", "const": v} }

func enumOf(values []string) obj {
	enum := make([]any, len(values))
	for i, v := range values {
		enum[i] = v
	}
	return obj{"type": "string", "enum": enum}
}

// object builds an object schema where every property is required, which is
// what structured outputs expect.
func object(properties props, required ...string) obj {
	req := make([]any, len(required))
	for i, r := range required {
		req[i] = r
	}
	return obj{"type": "object", "properties": properties, "required": req, "additionalProperties": false}
}

func attributeVariant(field string, ops []string, value obj) obj {
	return object(props{
		"type":  constant(typeAttribute),
		"field": constant(field),
		"op":    enumOf(ops),
		"value": value,
	}, "type", "field", "op", "value")
}

// scalarValue is the schema of one attribute value.
func scalarValue(s fieldSpec) obj {
	switch s.kind {
	case kindEnum:
		return enumOf(s.enum)
	case kindInt:
		return obj{"type": "integer", "description": "Whole number, 0 or more"}
	case kindMoney:
		return obj{"type": "number", "description": "Amount in TL, at most 2 decimal places"}
	}
	// The vocabulary is compiled in, so an unknown kind is a programming error
	// that TestSchema_matchesVocabulary hits on every run.
	panic(fmt.Sprintf("segment: no schema for field kind %d", s.kind))
}

// splitOps separates operators that take one value from those that take a list.
func splitOps(ops []string) (scalar, list []string) {
	for _, op := range ops {
		if slices.Contains([]string{opIn, opNotIn}, op) {
			list = append(list, op)
		} else {
			scalar = append(scalar, op)
		}
	}
	return scalar, list
}
