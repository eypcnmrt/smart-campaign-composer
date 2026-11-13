package segment

import (
	"errors"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	t.Parallel()
	if err := Validate(mustDecode(t, planExample)); err != nil {
		t.Errorf("Validate(plan example) = %v, want nil", err)
	}
}

func TestValidate_returnsAllErrors(t *testing.T) {
	t.Parallel()
	rule := mustDecode(t, ruleJSON(
		attrJSON("age", "gt", `3`),
		eventJSON("purchase", "performed", 400),
	))
	err := Validate(rule)
	verrs, ok := errors.AsType[ValidationErrors](err)
	if !ok || len(verrs) != 2 {
		t.Fatalf("Validate() = %v, want 2 ValidationErrors", err)
	}
	if verrs[0].Path != "conditions[0].field" || verrs[1].Path != "conditions[1].within_days" {
		t.Errorf("unexpected paths: %v", verrs)
	}
}

// Validate and Compile share one implementation, so they never disagree.
func TestValidate_agreesWithCompile(t *testing.T) {
	t.Parallel()
	inputs := []string{
		planExample,
		ruleJSON(),
		ruleJSON(attrJSON("city", "eq", `"Istanbul"`)),
		ruleJSON(eventJSON("purchase", "performed", 0)),
		ruleJSON(attrJSON("total_spent", "lt", `10.5`)),
	}
	for _, in := range inputs {
		rule := mustDecode(t, in)
		_, cErr := Compile(rule, testNow, 1)
		vErr := Validate(rule)
		if (cErr == nil) != (vErr == nil) {
			t.Errorf("disagreement on %s: Validate=%v Compile=%v", in, vErr, cErr)
		}
		if cErr != nil && cErr.Error() != vErr.Error() {
			t.Errorf("different messages on %s:\n%v\n%v", in, vErr, cErr)
		}
	}
}

func TestValidationErrors_Error(t *testing.T) {
	t.Parallel()
	v := ValidationErrors{
		{Path: "conditions[0].op", Msg: "bad"},
		{Path: "conditions[1]", Msg: "worse"},
	}
	want := "conditions[0].op: bad; conditions[1]: worse"
	if got := v.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !strings.Contains(FieldError{Path: "a.b", Msg: "m"}.Error(), "a.b: m") {
		t.Error("FieldError.Error() must include path and message")
	}
}
