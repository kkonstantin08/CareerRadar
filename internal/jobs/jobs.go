// Package jobs contains opportunity matching rules.
package jobs

import "strings"

// Job contains facts confirmed by an opportunity's source.
type Job struct {
	Level     string
	Direction string
	Location  string
	Type      string
	Language  string
	Company   string
	WorkMode  string
	Hours     *int
	Salary    *int64
	Currency  string
}

// Preferences restrict explicitly known opportunity attributes.
type Preferences struct {
	Levels     []string `json:"levels"`
	Directions []string `json:"directions"`
	Locations  []string `json:"locations"`
	Types      []string `json:"types"`
	Languages  []string `json:"languages"`
	Companies  []string `json:"companies"`
	WorkModes  []string `json:"work_modes"`
	MaxHours   *int     `json:"max_hours"`
	MinSalary  *int64   `json:"min_salary"`
	Currency   string   `json:"currency"`
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
	location := j.Location
	// Broad remote regions do not establish eligibility for a specific country.
	if len(p.Locations) > 0 && (strings.EqualFold(strings.TrimSpace(location), "worldwide") || strings.EqualFold(strings.TrimSpace(location), "anywhere")) {
		location = ""
	}
	for _, field := range []struct {
		value   string
		allowed []string
	}{
		{j.Level, p.Levels}, {j.Direction, p.Directions}, {location, p.Locations}, {j.Type, p.Types}, {j.Language, p.Languages}, {j.Company, p.Companies}, {j.WorkMode, p.WorkModes},
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
	if p.MaxHours != nil {
		if j.Hours == nil {
			unknown = true
		} else if *j.Hours > *p.MaxHours {
			return Unsuitable
		}
	}
	if p.MinSalary != nil {
		if j.Salary == nil || j.Currency == "" || p.Currency == "" || !strings.EqualFold(j.Currency, p.Currency) {
			unknown = true
		} else if *j.Salary < *p.MinSalary {
			return Unsuitable
		}
	}
	if unknown {
		return NeedsClarification
	}
	return Suitable
}
