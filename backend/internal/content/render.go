package content

import "strings"

// Fallbacks used when a variable's value is empty.
const (
	FallbackFirstName = "Değerli müşterimiz"
	FallbackCity      = "Türkiye"
)

// Vars holds the per-recipient values for the template variables.
type Vars struct {
	FirstName string
	City      string
}

// Render fills the variables in a template. An empty or blank value is replaced
// by its fallback, so "Merhaba {{first_name}}" never turns into "Merhaba ".
//
// Substitution is a single pass: a value that itself contains "{{city}}" is
// inserted literally and not expanded again.
func Render(template string, v Vars) string {
	return strings.NewReplacer(
		"{{"+VarFirstName+"}}", orFallback(v.FirstName, FallbackFirstName),
		"{{"+VarCity+"}}", orFallback(v.City, FallbackCity),
	).Replace(template)
}

// Render fills the variables in every field.
func (c Content) Render(v Vars) Content {
	return Content{
		SMS:          Render(c.SMS, v),
		EmailSubject: Render(c.EmailSubject, v),
		EmailBody:    Render(c.EmailBody, v),
	}
}

func orFallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
