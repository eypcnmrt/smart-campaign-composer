package segment

import "time"

// Validate checks a rule against the whitelist: field, operator and value
// compatibility, list and range limits, and the condition-count limits.
// It returns nil or a ValidationErrors with all problems.
//
// It shares one implementation with Compile, so a rule that validates always
// compiles and vice versa.
func Validate(r Rule) error {
	_, errs := build(r, defaultFields, time.Time{}, 1) // the clock only feeds an argument value
	if len(errs) == 0 {
		return nil
	}
	return errs
}
