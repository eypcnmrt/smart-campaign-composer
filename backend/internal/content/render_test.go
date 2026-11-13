package content

import "testing"

func TestRender(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		template string
		vars     Vars
		want     string
	}{
		{"both variables", "Merhaba {{first_name}}, {{city}} için fırsat", Vars{FirstName: "Ayşe", City: "İzmir"}, "Merhaba Ayşe, İzmir için fırsat"},
		{"repeated variable", "{{first_name}} {{first_name}}", Vars{FirstName: "Can"}, "Can Can"},
		{"no variables", "Kampanya başladı", Vars{FirstName: "Can"}, "Kampanya başladı"},
		{"empty first name", "Merhaba {{first_name}}!", Vars{City: "Bursa"}, "Merhaba Değerli müşterimiz!"},
		{"blank first name", "Merhaba {{first_name}}!", Vars{FirstName: " \t"}, "Merhaba Değerli müşterimiz!"},
		{"empty city", "{{city}} şubesi", Vars{FirstName: "Can"}, "Türkiye şubesi"},
		{"unknown variable stays literal", "Merhaba {{last_name}}", Vars{FirstName: "Can"}, "Merhaba {{last_name}}"},
		{"name is not trimmed", "Merhaba {{first_name}}", Vars{FirstName: " Can "}, "Merhaba  Can "},
		// One pass: a value that looks like a variable must not be expanded again.
		{"value that looks like a variable", "{{first_name}} / {{city}}", Vars{FirstName: "{{city}}", City: "Ankara"}, "{{city}} / Ankara"},
		{"empty template", "", Vars{FirstName: "Can"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Render(tt.template, tt.vars); got != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.template, got, tt.want)
			}
		})
	}
}

func TestContent_Render(t *testing.T) {
	t.Parallel()
	c := Content{SMS: "Merhaba {{first_name}}", EmailSubject: "{{city}} fırsatı", EmailBody: "Selam {{first_name}} ({{city}})"}
	got := c.Render(Vars{FirstName: "Ayşe", City: "Adana"})
	want := Content{SMS: "Merhaba Ayşe", EmailSubject: "Adana fırsatı", EmailBody: "Selam Ayşe (Adana)"}
	if got != want {
		t.Errorf("Render() = %+v, want %+v", got, want)
	}
	if c.SMS != "Merhaba {{first_name}}" {
		t.Error("Render() modified the receiver")
	}
}
