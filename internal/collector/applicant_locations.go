package collector

import (
	"sort"
	"strings"
)

func jobPostingRemote(posting map[string]any, places []map[string]any) bool {
	if raw, explicit := posting["jobLocationType"]; explicit {
		mode, _ := raw.(string)
		return strings.EqualFold(strings.TrimSpace(mode), "TELECOMMUTE")
	}
	// Older markup can publish the same flag as a PropertyValue on the work site.
	remote := false
	for _, place := range places {
		for _, property := range schemaObjects(place["additionalProperty"]) {
			name, _ := property["name"].(string)
			if !schemaTypeContains(property["@type"], "PropertyValue") || !strings.EqualFold(strings.TrimSpace(name), "TELECOMMUTE") {
				continue
			}
			flag, _ := property["value"].(bool)
			text, _ := property["value"].(string)
			if !flag && !strings.EqualFold(strings.TrimSpace(text), "true") {
				return false
			}
			remote = true
		}
	}
	return remote
}

// remoteApplicantLocations separates a complete filterable set from labels worth retaining as evidence.
func remoteApplicantLocations(posting map[string]any) (eligible, observed []string) {
	raw, explicit := posting["applicantLocationRequirements"]
	if !explicit {
		// Structured-data publishers can use explicit work-site countries as a remote fallback.
		raw = posting["jobLocation"]
	}
	areas, ok := raw.([]any)
	if !ok {
		areas = []any{raw}
	}
	complete := len(areas) > 0
	seen := make(map[string]bool)
	for _, area := range areas {
		item, _ := area.(map[string]any)
		name := ""
		if explicit {
			for _, kind := range []string{"Country", "AdministrativeArea", "State", "City"} {
				if schemaTypeContains(item["@type"], kind) {
					name, _ = item["name"].(string)
					break
				}
			}
		} else if address, ok := item["address"].(map[string]any); ok {
			switch country := address["addressCountry"].(type) {
			case string:
				name = country
			case map[string]any:
				if schemaTypeContains(country["@type"], "Country") {
					name, _ = country["name"].(string)
				}
			}
		}
		name = strings.TrimSpace(name)
		if name == "" {
			complete = false
		} else if !seen[name] {
			seen[name] = true
			observed = append(observed, name)
		}
	}
	sort.Strings(observed)
	if !complete {
		return []string{}, observed
	}
	return observed, observed
}
