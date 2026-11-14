package segment

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

const oneCondition = `{"type":"attribute","field":"city","op":"eq","value":"Ankara"}`

func ruleWithChannels(channels string) string {
	return `{"conditions":[` + oneCondition + `],"unsupported_criteria":[],"explanation":"x"` + channels + `}`
}

func TestDecode_channels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr string // part of the message, with the path
	}{
		{"absent means the brief names none", ruleWithChannels(``), nil, ""},
		{"empty array", ruleWithChannels(`,"channels":[]`), []string{}, ""},
		{"sms only", ruleWithChannels(`,"channels":["sms"]`), []string{"sms"}, ""},
		{"both, in the given order", ruleWithChannels(`,"channels":["email","sms"]`), []string{"email", "sms"}, ""},
		{"null", ruleWithChannels(`,"channels":null`), nil, "channels: must be an array of strings"},
		{"a string", ruleWithChannels(`,"channels":"sms"`), nil, "channels: must be an array of strings"},
		{"a number in the array", ruleWithChannels(`,"channels":["sms",3]`), nil, "channels: must be an array of strings"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Decode([]byte(tt.in))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Decode() error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if !reflect.DeepEqual(got.Channels, tt.want) {
				t.Fatalf("Channels = %#v, want %#v", got.Channels, tt.want)
			}
		})
	}
}

func TestValidate_channels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		channels string
		wantPath string // empty: valid
		wantMsg  string
	}{
		{"none", `[]`, "", ""},
		{"sms", `["sms"]`, "", ""},
		{"email", `["email"]`, "", ""},
		{"both", `["sms","email"]`, "", ""},
		{"unknown channel", `["sms","fax"]`, "channels[1]", `unknown channel "fax"; allowed: sms, email`},
		{"case matters", `["SMS"]`, "channels[0]", `unknown channel "SMS"`},
		{"listed twice", `["sms","email","sms"]`, "channels[2]", `channel "sms" is listed twice`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rule := mustDecode(t, ruleWithChannels(`,"channels":`+tt.channels))
			err := Validate(rule)
			if tt.wantPath == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			verrs, ok := errors.AsType[ValidationErrors](err)
			if !ok || len(verrs) != 1 || verrs[0].Path != tt.wantPath || !strings.Contains(verrs[0].Msg, tt.wantMsg) {
				t.Fatalf("Validate() = %v, want one error at %s containing %q", err, tt.wantPath, tt.wantMsg)
			}
			// Compile reports the same, so a rule that validates always compiles
			if _, cErr := Compile(rule, testNow, 1); cErr == nil || cErr.Error() != err.Error() {
				t.Fatalf("Compile() = %v, want the same error as Validate (%v)", cErr, err)
			}
		})
	}
}

// Channels never reach the SQL: they decide where the message goes, not who receives it.
func TestCompile_ignoresChannels(t *testing.T) {
	t.Parallel()
	with, err := Compile(mustDecode(t, ruleWithChannels(`,"channels":["sms"]`)), testNow, 1)
	if err != nil {
		t.Fatal(err)
	}
	without, err := Compile(mustDecode(t, ruleWithChannels(``)), testNow, 1)
	if err != nil {
		t.Fatal(err)
	}
	if with.SQL != without.SQL || !reflect.DeepEqual(with.Args, without.Args) {
		t.Fatalf("the channel changed the audience: %q vs %q", with.SQL, without.SQL)
	}
}

func TestChannels_returnsACopy(t *testing.T) {
	t.Parallel()
	got := Channels()
	if !reflect.DeepEqual(got, []string{"sms", "email"}) {
		t.Fatalf("Channels() = %v", got)
	}
	got[0] = "changed"
	if Channels()[0] != "sms" {
		t.Fatal("Channels() exposes the vocabulary")
	}
}
