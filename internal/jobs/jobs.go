// Package jobs contains opportunity matching rules.
package jobs

import "strings"

// Job contains facts confirmed by an opportunity's source.
type Job struct {
	Level     string
	Direction string
	Location  string
}

// Preferences restrict explicitly known opportunity attributes.
type Preferences struct {
	Levels     []string
	Directions []string
	Locations  []string
}

// Match describes whether an opportunity satisfies known preferences.
type Match string

const (
	Suitable           Match = "suitable"
	NeedsClarification Match = "needs_clarification"
	Unsuitable         Match = "unsuitable"
)

// Match retains opportunities with missing facts but rejects confirmed mismatches.
func (p Preferences) Match(j Job) Match {
	unknown := false
	for _, field := range []struct {
		value   string
		allowed []string
	}{
		{j.Level, p.Levels}, {j.Direction, p.Directions}, {j.Location, p.Locations},
	} {
		if len(field.allowed) == 0 {
			continue
		}
		if strings.TrimSpace(field.value) == "" {
			unknown = true
			continue
		}
		found := false
		for _, allowed := range field.allowed {
			if strings.EqualFold(strings.TrimSpace(field.value), strings.TrimSpace(allowed)) {
				found = true
				break
			}
		}
		if !found {
			return Unsuitable
		}
	}
	if unknown {
		return NeedsClarification
	}
	return Suitable
}
