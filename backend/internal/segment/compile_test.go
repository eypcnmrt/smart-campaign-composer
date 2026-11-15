package segment

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, time.November, 13, 12, 0, 0, 0, time.UTC)

func since(days int) time.Time { return testNow.Add(-time.Duration(days) * 24 * time.Hour) }

// JSON builders keep the tables readable.
func attrJSON(field, op, value string) string {
	return fmt.Sprintf(`{"type":"attribute","field":%q,"op":%q,"value":%s}`, field, op, value)
}

func eventJSON(event, op string, days int) string {
	return fmt.Sprintf(`{"type":"event","event":%q,"op":%q,"within_days":%d}`, event, op, days)
}

func groupJSON(conds ...string) string {
	return `{"type":"any_of","conditions":[` + strings.Join(conds, ",") + `]}`
}

func ruleJSON(conds ...string) string {
	return `{"conditions":[` + strings.Join(conds, ",") + `]}`
}

// withMeta adds the two properties every rule must carry (unsupported_criteria and explanation) to a test
// rule that only spells out its conditions. Input that already has them, or is not an object, is unchanged.
func withMeta(in string) string {
	if !strings.HasPrefix(in, "{") || strings.Contains(in, "unsupported_criteria") || strings.Contains(in, "explanation") {
		return in
	}
	if strings.HasPrefix(in, "{}") {
		return `{"unsupported_criteria":[],"explanation":"test"}`
	}
	return `{"unsupported_criteria":[],"explanation":"test",` + in[1:]
}

func mustDecode(t *testing.T, in string) Rule {
	t.Helper()
	r, err := Decode([]byte(withMeta(in)))
	if err != nil {
		t.Fatalf("Decode(%s) error = %v", in, err)
	}
	return r
}

const eventSQL = "EXISTS (SELECT 1 FROM events e WHERE e.user_id = u.id AND e.event_type = %s AND e.occurred_at >= %s)"

func TestCompile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		rule  string
		first int
		sql   string
		args  []any
	}{
		// Decision: an empty list is the explicit "no condition" rule and selects every user. A missing or null list is
		// not (Decode rejects it), so a half-written rule cannot widen into "everyone" by accident.
		{
			name: "no condition selects everyone",
			rule: ruleJSON(),
			sql:  "(TRUE)",
		},
		{
			name: "city eq",
			rule: ruleJSON(attrJSON("city", "eq", `"İstanbul"`)),
			sql:  "(u.city = $1)",
			args: []any{"İstanbul"},
		},
		{
			name: "language neq",
			rule: ruleJSON(attrJSON("language", "neq", `"en"`)),
			sql:  "(u.language <> $1)",
			args: []any{"en"},
		},
		{
			name: "city in",
			rule: ruleJSON(attrJSON("city", "in", `["İstanbul","Ankara"]`)),
			sql:  "(u.city = ANY($1))",
			args: []any{[]string{"İstanbul", "Ankara"}},
		},
		{
			name: "city not_in",
			rule: ruleJSON(attrJSON("city", "not_in", `["İzmir"]`)),
			sql:  "(u.city <> ALL($1))",
			args: []any{[]string{"İzmir"}},
		},
		{
			name: "total_orders eq",
			rule: ruleJSON(attrJSON("total_orders", "eq", `0`)),
			sql:  "(u.total_orders = $1)",
			args: []any{int64(0)},
		},
		{
			name: "total_orders neq",
			rule: ruleJSON(attrJSON("total_orders", "neq", `3`)),
			sql:  "(u.total_orders <> $1)",
			args: []any{int64(3)},
		},
		{
			name: "total_orders gt",
			rule: ruleJSON(attrJSON("total_orders", "gt", `3`)),
			sql:  "(u.total_orders > $1)",
			args: []any{int64(3)},
		},
		{
			name: "total_orders gte",
			rule: ruleJSON(attrJSON("total_orders", "gte", `3`)),
			sql:  "(u.total_orders >= $1)",
			args: []any{int64(3)},
		},
		{
			name: "total_orders lt",
			rule: ruleJSON(attrJSON("total_orders", "lt", `3`)),
			sql:  "(u.total_orders < $1)",
			args: []any{int64(3)},
		},
		{
			name: "total_orders lte",
			rule: ruleJSON(attrJSON("total_orders", "lte", `2147483647`)),
			sql:  "(u.total_orders <= $1)",
			args: []any{int64(2147483647)},
		},
		{
			name: "total_spent in kuruş, decimal",
			rule: ruleJSON(attrJSON("total_spent", "gt", `1500.5`)),
			sql:  "(u.total_spent_kurus > $1)",
			args: []any{int64(150050)},
		},
		{
			name: "total_spent in kuruş, whole",
			rule: ruleJSON(attrJSON("total_spent", "gte", `100`)),
			sql:  "(u.total_spent_kurus >= $1)",
			args: []any{int64(10000)},
		},
		{
			name: "total_spent one kuruş",
			rule: ruleJSON(attrJSON("total_spent", "eq", `0.01`)),
			sql:  "(u.total_spent_kurus = $1)",
			args: []any{int64(1)},
		},
		{
			name: "event performed",
			rule: ruleJSON(eventJSON("cart_add", "performed", 30)),
			sql:  "(" + fmt.Sprintf(eventSQL, "$1", "$2") + ")",
			args: []any{"cart_add", since(30)},
		},
		{
			name: "event not_performed",
			rule: ruleJSON(eventJSON("purchase", "not_performed", 365)),
			sql:  "(NOT " + fmt.Sprintf(eventSQL, "$1", "$2") + ")",
			args: []any{"purchase", since(365)},
		},
		{
			name: "event within one day",
			rule: ruleJSON(eventJSON("purchase", "performed", 1)),
			sql:  "(" + fmt.Sprintf(eventSQL, "$1", "$2") + ")",
			args: []any{"purchase", since(1)},
		},
		{
			name: "OR group of two cities",
			rule: ruleJSON(groupJSON(attrJSON("city", "eq", `"İstanbul"`), attrJSON("city", "eq", `"Kocaeli"`))),
			sql:  "((u.city = $1 OR u.city = $2))",
			args: []any{"İstanbul", "Kocaeli"},
		},
		{
			name: "OR group of one condition",
			rule: ruleJSON(groupJSON(attrJSON("language", "eq", `"tr"`))),
			sql:  "((u.language = $1))",
			args: []any{"tr"},
		},
		{
			name: "OR group mixing event and attribute",
			rule: ruleJSON(groupJSON(eventJSON("purchase", "performed", 7), attrJSON("total_orders", "gte", `5`))),
			sql:  "((" + fmt.Sprintf(eventSQL, "$1", "$2") + " OR u.total_orders >= $3))",
			args: []any{"purchase", since(7), int64(5)},
		},
		{
			name: "several top-level conditions are ANDed",
			rule: ruleJSON(attrJSON("language", "eq", `"tr"`), attrJSON("total_orders", "gt", `1`)),
			sql:  "(u.language = $1 AND u.total_orders > $2)",
			args: []any{"tr", int64(1)},
		},
		{
			name:  "plan example numbered from 5",
			rule:  planExample,
			first: 5,
			sql: "(" + fmt.Sprintf(eventSQL, "$5", "$6") + " AND NOT " + fmt.Sprintf(eventSQL, "$7", "$8") +
				" AND (u.city = $9 OR u.city = $10))",
			args: []any{"cart_add", since(30), "purchase", since(30), "İstanbul", "Kocaeli"},
		},
		{
			name:  "numbering from 12",
			rule:  ruleJSON(attrJSON("city", "in", `["Ankara","Bursa"]`)),
			first: 12,
			sql:   "(u.city = ANY($12))",
			args:  []any{[]string{"Ankara", "Bursa"}},
		},
		{
			name: "ten conditions are allowed",
			rule: ruleJSON(slices.Repeat([]string{attrJSON("language", "eq", `"tr"`)}, 10)...),
			sql:  "(" + strings.TrimSuffix(strings.Repeat("u.language = $%d AND ", 10), " AND ") + ")",
			args: slices.Repeat([]any{"tr"}, 10),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			first := tt.first
			if first == 0 {
				first = 1
			}
			wantSQL := tt.sql
			if strings.Contains(wantSQL, "$%d") { // numbered placeholders for the 10-condition case
				nums := make([]any, 10)
				for i := range nums {
					nums[i] = i + 1
				}
				wantSQL = fmt.Sprintf(wantSQL, nums...)
			}
			got, err := Compile(mustDecode(t, tt.rule), testNow, first)
			if err != nil {
				t.Fatalf("Compile() error = %v", err)
			}
			if got.SQL != wantSQL {
				t.Errorf("SQL:\n got %s\nwant %s", got.SQL, wantSQL)
			}
			if !reflect.DeepEqual(got.Args, tt.args) {
				t.Errorf("Args:\n got %#v\nwant %#v", got.Args, tt.args)
			}
		})
	}
}

// The time bound is computed from now in Go: same inputs, same output, and
// the SQL never reads the database clock.
func TestCompile_isDeterministicAndNeverUsesSQLClock(t *testing.T) {
	t.Parallel()
	rule := mustDecode(t, ruleJSON(eventJSON("cart_add", "performed", 30), eventJSON("purchase", "not_performed", 30)))
	a, errA := Compile(rule, testNow, 1)
	b, errB := Compile(rule, testNow, 1)
	if errA != nil || errB != nil {
		t.Fatalf("Compile() errors = %v, %v", errA, errB)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("same input produced different fragments:\n%+v\n%+v", a, b)
	}
	if strings.Contains(strings.ToLower(a.SQL), "now()") || strings.Contains(strings.ToLower(a.SQL), "interval") {
		t.Errorf("SQL must not read the database clock: %s", a.SQL)
	}

	later, err := Compile(rule, testNow.Add(time.Hour), 1)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if later.Args[1].(time.Time).Sub(a.Args[1].(time.Time)) != time.Hour {
		t.Error("time bound must move with now")
	}
}

func TestCompile_timeBoundIsUTC(t *testing.T) {
	t.Parallel()
	istanbul := time.FixedZone("Europe/Istanbul", 3*60*60)
	got, err := Compile(mustDecode(t, ruleJSON(eventJSON("purchase", "performed", 2))), testNow.In(istanbul), 1)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	bound := got.Args[1].(time.Time)
	if !bound.Equal(since(2)) || bound.Location() != time.UTC {
		t.Errorf("bound = %v (%v), want %v in UTC", bound, bound.Location(), since(2))
	}
}

func TestCompile_argumentErrors(t *testing.T) {
	t.Parallel()
	rule := mustDecode(t, ruleJSON(attrJSON("city", "eq", `"Ankara"`)))
	tests := []struct {
		name  string
		now   time.Time
		first int
		want  error
	}{
		{"first parameter 0", testNow, 0, ErrInvalidParamIndex},
		{"first parameter negative", testNow, -3, ErrInvalidParamIndex},
		{"zero now", time.Time{}, 1, ErrZeroTime},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Compile(rule, tt.now, tt.first); !errors.Is(err, tt.want) {
				t.Errorf("Compile() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestCompile_invalidRules(t *testing.T) {
	t.Parallel()
	cityList := strings.Join(Cities(), ", ")
	tooMany := slices.Repeat([]string{attrJSON("language", "eq", `"tr"`)}, 11)
	const (
		intMsg   = "must be a whole number between 0 and 2147483647"
		moneyMsg = "must be a non-negative amount in TL with at most 2 decimal places"
	)
	tests := []struct {
		name string
		rule string
		want []FieldError
	}{
		// An empty list of conditions is a rule for everyone (see TestCompile); a group still needs a member.
		{
			name: "empty any_of group",
			rule: ruleJSON(groupJSON()),
			want: []FieldError{{"conditions[0].conditions", "must contain at least one condition"}},
		},
		// Decision: canonical values only; dotless I is rejected, not matched to nobody.
		{
			name: "dotless Istanbul",
			rule: ruleJSON(attrJSON("city", "eq", `"Istanbul"`)),
			want: []FieldError{{"conditions[0].value", `"Istanbul" is not an allowed value; allowed: ` + cityList}},
		},
		{
			name: "lower-case city",
			rule: ruleJSON(attrJSON("city", "eq", `"ankara"`)),
			want: []FieldError{{"conditions[0].value", `"ankara" is not an allowed value; allowed: ` + cityList}},
		},
		{
			name: "unknown language in list",
			rule: ruleJSON(attrJSON("language", "in", `["tr","de"]`)),
			want: []FieldError{{"conditions[0].value[1]", `"de" is not an allowed value; allowed: tr, en`}},
		},
		{
			name: "city value is a number",
			rule: ruleJSON(attrJSON("city", "eq", `34`)),
			want: []FieldError{{"conditions[0].value", "must be a string"}},
		},
		{
			name: "unknown field",
			rule: ruleJSON(attrJSON("age", "gt", `30`)),
			want: []FieldError{{"conditions[0].field", `unknown field "age"; allowed: city, language, total_orders, total_spent`}},
		},
		{
			name: "operator not allowed for text field",
			rule: ruleJSON(attrJSON("city", "gt", `"Ankara"`)),
			want: []FieldError{{"conditions[0].op", `operator "gt" is not allowed for city; allowed: eq, neq, in, not_in`}},
		},
		{
			name: "list operator on numeric field",
			rule: ruleJSON(attrJSON("total_orders", "in", `[1,2]`)),
			want: []FieldError{{"conditions[0].op", `operator "in" is not allowed for total_orders; allowed: eq, neq, gt, gte, lt, lte`}},
		},
		{
			name: "unknown operator",
			rule: ruleJSON(attrJSON("city", "like", `"A%"`)),
			want: []FieldError{{"conditions[0].op", `operator "like" is not allowed for city; allowed: eq, neq, in, not_in`}},
		},
		{
			name: "in needs an array",
			rule: ruleJSON(attrJSON("city", "in", `"Ankara"`)),
			want: []FieldError{{"conditions[0].value", "must be an array for operator in"}},
		},
		{
			name: "not_in needs an array",
			rule: ruleJSON(attrJSON("city", "not_in", `"Ankara"`)),
			want: []FieldError{{"conditions[0].value", "must be an array for operator not_in"}},
		},
		{
			name: "empty in list",
			rule: ruleJSON(attrJSON("city", "in", `[]`)),
			want: []FieldError{{"conditions[0].value", "must contain between 1 and 50 values, got 0"}},
		},
		{
			name: "oversized in list",
			rule: ruleJSON(attrJSON("city", "in", "["+strings.TrimSuffix(strings.Repeat(`"Ankara",`, 51), ",")+"]")),
			want: []FieldError{{"conditions[0].value", "must contain between 1 and 50 values, got 51"}},
		},
		{
			name: "scalar operator with an array",
			rule: ruleJSON(attrJSON("city", "eq", `["Ankara"]`)),
			want: []FieldError{{"conditions[0].value", "must be a string"}},
		},
		{
			name: "null value",
			rule: ruleJSON(attrJSON("language", "eq", `null`)),
			want: []FieldError{{"conditions[0].value", "must be a string"}},
		},
		{name: "negative total_orders", rule: ruleJSON(attrJSON("total_orders", "gt", `-1`)), want: []FieldError{{"conditions[0].value", intMsg}}},
		{name: "fractional total_orders", rule: ruleJSON(attrJSON("total_orders", "gt", `1.5`)), want: []FieldError{{"conditions[0].value", intMsg}}},
		{name: "string total_orders", rule: ruleJSON(attrJSON("total_orders", "gt", `"3"`)), want: []FieldError{{"conditions[0].value", intMsg}}},
		{name: "total_orders beyond int32", rule: ruleJSON(attrJSON("total_orders", "gt", `2147483648`)), want: []FieldError{{"conditions[0].value", intMsg}}},
		{name: "negative total_spent", rule: ruleJSON(attrJSON("total_spent", "gt", `-5`)), want: []FieldError{{"conditions[0].value", moneyMsg}}},
		{name: "three decimals total_spent", rule: ruleJSON(attrJSON("total_spent", "gt", `12.345`)), want: []FieldError{{"conditions[0].value", moneyMsg}}},
		{name: "exponent total_spent", rule: ruleJSON(attrJSON("total_spent", "gt", `1e3`)), want: []FieldError{{"conditions[0].value", moneyMsg}}},
		{name: "string total_spent", rule: ruleJSON(attrJSON("total_spent", "gt", `"100"`)), want: []FieldError{{"conditions[0].value", moneyMsg}}},
		{
			name: "unknown event",
			rule: ruleJSON(eventJSON("refund", "performed", 5)),
			want: []FieldError{{"conditions[0].event", `unknown event "refund"; allowed: cart_add, purchase`}},
		},
		{
			name: "attribute operator on an event",
			rule: ruleJSON(eventJSON("purchase", "eq", 5)),
			want: []FieldError{{"conditions[0].op", `operator "eq" is not allowed for events; allowed: performed, not_performed`}},
		},
		{name: "within_days 0", rule: ruleJSON(eventJSON("purchase", "performed", 0)), want: []FieldError{{"conditions[0].within_days", "must be between 1 and 365"}}},
		{name: "within_days negative", rule: ruleJSON(eventJSON("purchase", "performed", -7)), want: []FieldError{{"conditions[0].within_days", "must be between 1 and 365"}}},
		{name: "within_days 366", rule: ruleJSON(eventJSON("purchase", "performed", 366)), want: []FieldError{{"conditions[0].within_days", "must be between 1 and 365"}}},
		{
			name: "eleven top-level conditions",
			rule: ruleJSON(tooMany...),
			want: []FieldError{{"conditions", "must contain at most 10 conditions, got 11"}},
		},
		{
			name: "eleven conditions in a group",
			rule: ruleJSON(attrJSON("language", "eq", `"tr"`), groupJSON(tooMany...)),
			want: []FieldError{{"conditions[1].conditions", "must contain at most 10 conditions, got 11"}},
		},
		{
			name: "every problem is reported with its path",
			rule: ruleJSON(
				attrJSON("city", "gt", `"x"`),
				eventJSON("cart_add", "performed", 0),
				groupJSON(attrJSON("total_orders", "eq", `-1`), attrJSON("city", "eq", `"Istanbul"`)),
			),
			want: []FieldError{
				{"conditions[0].op", `operator "gt" is not allowed for city; allowed: eq, neq, in, not_in`},
				{"conditions[1].within_days", "must be between 1 and 365"},
				{"conditions[2].conditions[0].value", "must be a whole number between 0 and 2147483647"},
				{"conditions[2].conditions[1].value", `"Istanbul" is not an allowed value; allowed: ` + strings.Join(Cities(), ", ")},
			},
		},
		{
			name: "event reports all of its problems",
			rule: ruleJSON(eventJSON("refund", "eq", 999)),
			want: []FieldError{
				{"conditions[0].event", `unknown event "refund"; allowed: cart_add, purchase`},
				{"conditions[0].op", `operator "eq" is not allowed for events; allowed: performed, not_performed`},
				{"conditions[0].within_days", "must be between 1 and 365"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Compile(mustDecode(t, tt.rule), testNow, 1)
			verrs, ok := errors.AsType[ValidationErrors](err)
			if !ok {
				t.Fatalf("Compile() error = %v, want ValidationErrors", err)
			}
			if !slices.Equal(verrs, ValidationErrors(tt.want)) {
				t.Errorf("Compile() errors:\n got %v\nwant %v", verrs, tt.want)
			}
		})
	}
}

// Rules built in Go, not decoded, can hold nil nodes; they must be reported,
// not dereferenced.
func TestCompile_nilNodes(t *testing.T) {
	t.Parallel()
	rule := Rule{Conditions: []Node{
		(*Attribute)(nil),
		nil,
		(*AnyOf)(nil),
		&AnyOf{Conditions: []Condition{nil, (*Event)(nil)}},
		(*Event)(nil),
	}}
	_, err := Compile(rule, testNow, 1)
	verrs, ok := errors.AsType[ValidationErrors](err)
	if !ok {
		t.Fatalf("Compile() error = %v, want ValidationErrors", err)
	}
	want := ValidationErrors{
		{"conditions[0]", "must not be null"},
		{"conditions[1]", "must be an attribute, event or any_of condition"},
		{"conditions[2]", "must not be null"},
		{"conditions[3].conditions[0]", "must be an attribute or event condition"},
		{"conditions[3].conditions[1]", "must not be null"},
		{"conditions[4]", "must not be null"},
	}
	if !slices.Equal(verrs, want) {
		t.Errorf("errors:\n got %v\nwant %v", verrs, want)
	}
}

func TestCompile_zeroRuleIsRejected(t *testing.T) {
	t.Parallel()
	frag, err := Compile(Rule{}, testNow, 1)
	if err == nil {
		t.Fatalf("Compile(Rule{}) = %+v, want an error: a rule whose conditions were never set must never match everyone", frag)
	}
	if frag.SQL != "" || frag.Args != nil {
		t.Errorf("failed Compile returned a fragment: %+v", frag)
	}
	// Only an empty, non-nil list is the rule without a condition (Decode makes one for "conditions": []).
	if frag, err := Compile(Rule{Conditions: []Node{}}, testNow, 1); err != nil || frag.SQL != "(TRUE)" {
		t.Errorf("Compile(Rule{Conditions: []Node{}}) = %+v, %v; want (TRUE)", frag, err)
	}
}

// ---- property checks shared by the fuzz tests ----

var (
	safeSQLChars   = regexp.MustCompile(`^[A-Za-z0-9_$\s().=<>]*$`)
	wordRE         = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	placeholderRE  = regexp.MustCompile(`\$(\d+)`)
	allowedSQLWord = map[string]bool{
		"select": true, "from": true, "where": true, "and": true, "or": true, "not": true,
		"exists": true, "any": true, "all": true, "events": true, "e": true, "u": true,
		"user_id": true, "id": true, "event_type": true, "occurred_at": true,
		"city": true, "language": true, "total_orders": true, "total_spent_kurus": true,
	}
)

// assertSafeSQL fails unless sql is built only from known tokens and
// placeholders numbered first, first+1, ... one per argument, in order.
func assertSafeSQL(t *testing.T, frag Fragment, first int) {
	t.Helper()
	if !safeSQLChars.MatchString(frag.SQL) {
		t.Fatalf("SQL has characters outside the allowed set: %q", frag.SQL)
	}
	for _, w := range wordRE.FindAllString(frag.SQL, -1) {
		if !allowedSQLWord[strings.ToLower(w)] {
			t.Fatalf("SQL has unexpected word %q: %q", w, frag.SQL)
		}
	}
	stripped := strings.ReplaceAll(placeholderRE.ReplaceAllString(frag.SQL, ""), "SELECT 1", "SELECT")
	if strings.ContainsAny(stripped, "0123456789") {
		t.Fatalf("SQL has a literal number outside placeholders: %q", frag.SQL)
	}
	matches := placeholderRE.FindAllStringSubmatch(frag.SQL, -1)
	if len(matches) != len(frag.Args) {
		t.Fatalf("%d placeholders for %d args: %q", len(matches), len(frag.Args), frag.SQL)
	}
	for i, m := range matches {
		if n, _ := strconv.Atoi(m[1]); n != first+i {
			t.Fatalf("placeholder %d is $%s, want $%d: %q", i, m[1], first+i, frag.SQL)
		}
	}
}

// flatten collects every string held by the arguments.
func flatten(args []any) map[string]bool {
	out := map[string]bool{}
	for _, a := range args {
		switch v := a.(type) {
		case string:
			out[v] = true
		case []string:
			for _, s := range v {
				out[s] = true
			}
		}
	}
	return out
}

func FuzzCompile(f *testing.F) {
	seeds := []string{
		planExample,
		ruleJSON(attrJSON("city", "eq", `"'; DROP TABLE users;--"`)),
		ruleJSON(attrJSON("city", "in", `["Ankara","' OR '1'='1"]`)),
		ruleJSON(attrJSON("total_spent", "gt", `1e999`)),
		ruleJSON(attrJSON("total_orders", "eq", `99999999999999999999`)),
		ruleJSON(eventJSON("purchase'; DROP TABLE events;--", "performed", 3)),
		ruleJSON(attrJSON("city; DROP TABLE users", "eq", `"Ankara"`)),
		ruleJSON(groupJSON(attrJSON("language", "eq", `"tr"`), eventJSON("cart_add", "not_performed", 365))),
		`{"conditions":[{"type":"any_of","conditions":[{"type":"any_of","conditions":[]}]}]}`,
		`{"conditions":[{"type":"attribute","field":"city","op":"eq","value":"Ankara"}],"x":"'; DROP TABLE users;--"}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s), uint8(0))
	}
	f.Fuzz(func(t *testing.T, data []byte, firstParam uint8) {
		rule, err := Decode(data)
		if err != nil {
			return
		}
		first := int(firstParam) + 1
		frag, err := Compile(rule, testNow, first)
		if err != nil {
			if frag.SQL != "" || frag.Args != nil {
				t.Fatalf("failed Compile returned a fragment: %+v", frag)
			}
			return
		}
		assertSafeSQL(t, frag, first)

		// Every text value of an accepted rule must travel as an argument.
		have := flatten(frag.Args)
		for _, n := range rule.Conditions {
			for _, a := range attributesOf(n) {
				switch v := a.Value.(type) {
				case string:
					if !have[v] {
						t.Fatalf("value %q is missing from args", v)
					}
				case []any:
					for _, el := range v {
						if s, ok := el.(string); ok && !have[s] {
							t.Fatalf("list value %q is missing from args", s)
						}
					}
				}
			}
		}
	})
}

func attributesOf(n Node) []*Attribute {
	switch n := n.(type) {
	case *Attribute:
		return []*Attribute{n}
	case *AnyOf:
		var out []*Attribute
		for _, c := range n.Conditions {
			if a, ok := c.(*Attribute); ok {
				out = append(out, a)
			}
		}
		return out
	}
	return nil
}

// FuzzCompileValues puts arbitrary text into every value position of a rule
// whose whitelist accepts it. The compiler must still produce exactly the SQL
// it produces for an innocuous value, with the payload only in args: SQL text
// depends on the rule's shape, never on its values.
func FuzzCompileValues(f *testing.F) {
	for _, s := range []string{"x", "", "'; DROP TABLE users;--", `" OR 1=1 --`, "$1", "$99", "?", "\x00", "\\", "%", "1); DELETE FROM events; --", "İstanbul"} {
		f.Add(s, uint8(0))
	}
	makeCase := func(payload string) (fieldSet, Rule) {
		fs := fieldSet{"city": {column: "u.city", kind: kindEnum, ops: enumOps, enum: []string{payload}}}
		cond := func(op string, v any) *Attribute { return &Attribute{Field: "city", Op: op, Value: v} }
		list := []any{payload}
		return fs, Rule{Conditions: []Node{
			cond(opEq, payload),
			cond(opNeq, payload),
			cond(opIn, list),
			cond(opNotIn, list),
			&AnyOf{Conditions: []Condition{cond(opEq, payload), cond(opIn, list)}},
		}}
	}
	f.Fuzz(func(t *testing.T, payload string, firstParam uint8) {
		first := int(firstParam) + 1
		fsFuzz, ruleFuzz := makeCase(payload)
		got, err := compile(ruleFuzz, fsFuzz, testNow, first)
		if err != nil {
			t.Fatalf("compile() with payload %q: %v", payload, err)
		}
		fsRef, ruleRef := makeCase("x")
		ref, err := compile(ruleRef, fsRef, testNow, first)
		if err != nil {
			t.Fatalf("compile() reference: %v", err)
		}
		if got.SQL != ref.SQL {
			t.Fatalf("SQL depends on the value %q:\n got %s\nref %s", payload, got.SQL, ref.SQL)
		}
		assertSafeSQL(t, got, first)
		if !flatten(got.Args)[payload] {
			t.Fatalf("payload %q is missing from args %#v", payload, got.Args)
		}
	})
}
