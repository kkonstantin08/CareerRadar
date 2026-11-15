// Package jobs contains opportunity matching rules.
package jobs

import (
	"slices"
	"strings"
)

// Job contains facts confirmed by an opportunity's source.
type Job struct {
	Level              string
	Direction          string
	Location           string
	ApplicantLocations []string
	Type               string
	Language           string
	Company            string
	WorkMode           string
	Hours              *int
	Salary             *int64
	SalaryMin          *int64
	SalaryMax          *int64
	Currency           string
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
	MaxSalary  *int64   `json:"max_salary"`
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
	locations := make([]string, len(p.Locations))
	for i, value := range p.Locations {
		locations[i] = normalizeLocation(value)
	}
	location = normalizeLocation(location)
	if j.WorkMode == "remote" && j.ApplicantLocations != nil && len(locations) > 0 {
		switch matchApplicantLocations(j.ApplicantLocations, locations) {
		case Unsuitable:
			return Unsuitable
		case NeedsClarification:
			unknown = true
		}
		locations = nil
	}
	// Broad remote regions do not establish eligibility for a specific country.
	if len(p.Locations) > 0 && (strings.EqualFold(strings.TrimSpace(location), "worldwide") || strings.EqualFold(strings.TrimSpace(location), "anywhere")) {
		location = ""
	}
	if location == "россия" {
		countryAllowed, cityAllowed := false, false
		for _, allowed := range locations {
			if allowed == "россия" {
				countryAllowed = true
			} else if allowed != "" {
				cityAllowed = true
			}
		}
		// Country-level eligibility is useful for remote work but does not identify a permitted office city.
		if !countryAllowed || j.WorkMode != "remote" && cityAllowed {
			location = ""
		}
	}
	for _, field := range []struct {
		value   string
		allowed []string
	}{
		{j.Level, p.Levels}, {j.Direction, p.Directions}, {location, locations}, {j.Type, p.Types}, {j.Company, p.Companies}, {j.WorkMode, p.WorkModes},
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
	if len(p.Languages) > 0 {
		if strings.TrimSpace(j.Language) == "" {
			unknown = true
		} else {
			found := false
			for _, actual := range strings.Split(j.Language, ",") {
				for _, allowed := range p.Languages {
					if strings.EqualFold(strings.TrimSpace(actual), strings.TrimSpace(allowed)) {
						found = true
					}
				}
			}
			if !found {
				return Unsuitable
			}
		}
	}
	if p.MaxHours != nil {
		if j.Hours == nil {
			unknown = true
		} else if *j.Hours > *p.MaxHours {
			return Unsuitable
		}
	}
	if p.MinSalary != nil || p.MaxSalary != nil {
		if j.Currency == "" || p.Currency == "" || !strings.EqualFold(j.Currency, p.Currency) {
			unknown = true
		} else if j.Salary != nil {
			if (p.MinSalary != nil && *j.Salary < *p.MinSalary) || (p.MaxSalary != nil && *j.Salary > *p.MaxSalary) {
				return Unsuitable
			}
		} else if j.SalaryMin != nil && j.SalaryMax != nil && *j.SalaryMin > *j.SalaryMax {
			unknown = true
		} else {
			if (p.MinSalary != nil && j.SalaryMax != nil && *j.SalaryMax < *p.MinSalary) || (p.MaxSalary != nil && j.SalaryMin != nil && *j.SalaryMin > *p.MaxSalary) {
				return Unsuitable
			}
			if (p.MinSalary != nil && (j.SalaryMin == nil || *j.SalaryMin < *p.MinSalary)) || (p.MaxSalary != nil && (j.SalaryMax == nil || *j.SalaryMax > *p.MaxSalary)) {
				unknown = true
			}
		}
	}
	if unknown {
		return NeedsClarification
	}
	return Suitable
}

func matchApplicantLocations(actual, allowed []string) Match {
	unknown := len(actual) == 0
	for _, candidate := range actual {
		candidate = normalizeLocation(candidate)
		if candidate == "" || strings.EqualFold(candidate, "worldwide") || strings.EqualFold(candidate, "anywhere") {
			unknown = true
			continue
		}
		for _, location := range allowed {
			if slices.Contains(ApplicantLocationAliases(location), candidate) {
				return Suitable
			}
		}
	}
	if unknown {
		return NeedsClarification
	}
	return Unsuitable
}

func normalizeLocation(value string) string {
	return LocationAliases(value)[0]
}

// LocationAliases returns the normalized spellings used by matching and the catalog facet.
func LocationAliases(value string) []string {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, spellings := range [][]string{
		{"россия", "russia", "russian federation", "ru", "рф", "российская федерация"},
		{"казань", "kazan"},
		{"иннополис", "innopolis"},
	} {
		for _, spelling := range spellings {
			if value == spelling {
				return spellings
			}
		}
	}
	return []string{value}
}

// ApplicantLocationAliases includes nationwide eligibility for the configured Russian cities.
func ApplicantLocationAliases(value string) []string {
	aliases := LocationAliases(value)
	if aliases[0] == "казань" || aliases[0] == "иннополис" {
		return slices.Concat(aliases, LocationAliases("россия"))
	}
	return aliases
}
