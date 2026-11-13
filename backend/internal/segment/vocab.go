package segment

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Operator names as they appear in rule JSON.
const (
	opEq           = "eq"
	opNeq          = "neq"
	opIn           = "in"
	opNotIn        = "not_in"
	opGt           = "gt"
	opGte          = "gte"
	opLt           = "lt"
	opLte          = "lte"
	opPerformed    = "performed"
	opNotPerformed = "not_performed"
)

// Limits enforced by validation. They cannot be expressed in the JSON schema
// handed to the LLM, so the validator is the only place they live.
const (
	maxConditions = 10 // top-level list and each any_of group
	maxListValues = 50 // elements of an in / not_in array
	maxWithinDays = 365
	maxOrders     = 1<<31 - 1 // total_orders is a 32-bit column
)

var (
	// cities and languages are the canonical value lists. Seed data, the
	// validator and (later) the LLM prompt all read them through Cities and
	// Languages. Matching is exact: "Istanbul" (dotless I) is rejected rather
	// than silently matching zero users.
	cities    = []string{"İstanbul", "Ankara", "İzmir", "Bursa", "Antalya", "Adana", "Konya", "Kocaeli", "Gaziantep", "Kayseri", "Eskişehir", "Samsun"}
	languages = []string{"tr", "en"}

	eventTypes = []string{"cart_add", "purchase"}
	eventOps   = []string{opPerformed, opNotPerformed}

	enumOps    = []string{opEq, opNeq, opIn, opNotIn}
	numericOps = []string{opEq, opNeq, opGt, opGte, opLt, opLte}
)

// Cities returns the canonical city list.
func Cities() []string { return slices.Clone(cities) }

// Languages returns the canonical language list.
func Languages() []string { return slices.Clone(languages) }

type kind int

const (
	kindEnum  kind = iota // string from a fixed list
	kindInt               // whole number, 0..maxOrders
	kindMoney             // TL amount in JSON, compared as int64 kuruş
)

type fieldSpec struct {
	column string // trusted SQL column; the only way a name reaches SQL
	kind   kind
	ops    []string
	enum   []string // allowed values when kind is kindEnum
}

type fieldSet map[string]fieldSpec

// defaultFields is the attribute whitelist.
//
// Schema contract: every column below is NOT NULL (total_orders and
// total_spent_kurus default to 0, city and language are always seeded).
// neq and not_in compile to <> and <> ALL, which are NULL (not true) for a
// NULL column, so nullable columns would silently drop users from the
// segment. The migration must declare these columns NOT NULL.
//
// total_spent is written in TL in rule JSON (at most 2 decimals) and stored
// and compared as BIGINT kuruş, so no floating point is involved.
var defaultFields = fieldSet{
	"city":         {column: "u.city", kind: kindEnum, ops: enumOps, enum: cities},
	"language":     {column: "u.language", kind: kindEnum, ops: enumOps, enum: languages},
	"total_orders": {column: "u.total_orders", kind: kindInt, ops: numericOps},
	"total_spent":  {column: "u.total_spent_kurus", kind: kindMoney, ops: numericOps},
}

// comparators maps an operator to its SQL shape; %s is the placeholder.
var comparators = map[string]string{
	opEq:    "= %s",
	opNeq:   "<> %s",
	opGt:    "> %s",
	opGte:   ">= %s",
	opLt:    "< %s",
	opLte:   "<= %s",
	opIn:    "= ANY(%s)",
	opNotIn: "<> ALL(%s)",
}

func (fs fieldSet) names() []string {
	out := make([]string, 0, len(fs))
	for name := range fs {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// scalar converts one JSON value to the SQL argument for the field. The
// second result is a validation message, empty when the value is acceptable.
func (s fieldSpec) scalar(v any) (arg any, msg string) {
	switch s.kind {
	case kindEnum:
		str, ok := v.(string)
		if !ok {
			return nil, "must be a string"
		}
		if !slices.Contains(s.enum, str) {
			return nil, fmt.Sprintf("%s is not an allowed value; allowed: %s", shorten(str), strings.Join(s.enum, ", "))
		}
		return str, ""
	case kindInt:
		if n, ok := v.(json.Number); ok {
			if i, err := strconv.ParseInt(n.String(), 10, 64); err == nil && i >= 0 && i <= maxOrders {
				return i, ""
			}
		}
		return nil, fmt.Sprintf("must be a whole number between 0 and %d", maxOrders)
	case kindMoney:
		if n, ok := v.(json.Number); ok {
			if k, ok := parseKurus(n.String()); ok {
				return k, ""
			}
		}
		return nil, "must be a non-negative amount in TL with at most 2 decimal places"
	}
	return nil, "unsupported field kind"
}

// parseKurus converts a plain decimal such as "1500.5" to kuruş (150050)
// without floating point. Exponents, signs and more than 2 decimals are
// rejected; the integer part is capped so the result fits int64.
func parseKurus(s string) (int64, bool) {
	whole, frac, hasDot := strings.Cut(s, ".")
	if whole == "" || len(whole) > 12 || !digitsOnly(whole) {
		return 0, false
	}
	if hasDot && (frac == "" || len(frac) > 2 || !digitsOnly(frac)) {
		return 0, false
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, false
	}
	var f int64
	if hasDot {
		f, err = strconv.ParseInt((frac + "0")[:2], 10, 64)
		if err != nil {
			return 0, false
		}
	}
	return w*100 + f, true
}

func digitsOnly(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// clip bounds untrusted text echoed back in error messages.
func clip(s string) string {
	const limit = 40
	if r := []rune(s); len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return s
}

// shorten quotes and clips untrusted text for an error message.
func shorten(s string) string { return strconv.Quote(clip(s)) }
