package jobs

import "testing"

func TestMatchPreservesUnknownAndRejectsExplicitMismatch(t *testing.T) {
	p := Preferences{Levels: []string{"Junior"}, Directions: []string{"Backend"}, Locations: []string{"Казань"}}
	cases := []struct {
		name string
		job  Job
		want Match
	}{
		{"match", Job{Level: "Junior", Direction: "Backend", Location: "Казань"}, Suitable},
		{"unknown", Job{Level: "Junior", Direction: "Backend"}, NeedsClarification},
		{"level mismatch", Job{Level: "Senior", Direction: "Backend", Location: "Казань"}, Unsuitable},
		{"direction mismatch", Job{Level: "Junior", Direction: "QA", Location: "Казань"}, Unsuitable},
		{"location mismatch", Job{Level: "Junior", Direction: "Backend", Location: "Москва"}, Unsuitable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.Match(tc.job); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMatchNumericAndMultiplePreferences(t *testing.T) {
	max := 30
	salary := int64(60000)
	p := Preferences{Levels: []string{"Intern", "Junior"}, Languages: []string{"Go", "Python"}, WorkModes: []string{"remote"}, MaxHours: &max, MinSalary: &salary, Currency: "RUB"}
	hours := 20
	pay := int64(80000)
	good := Job{Level: "Junior", Language: "Go", WorkMode: "remote", Hours: &hours, Salary: &pay, Currency: "RUB"}
	if p.Match(good) != Suitable {
		t.Fatal("expected suitable")
	}
	hours = 40
	if p.Match(good) != Unsuitable {
		t.Fatal("hours mismatch accepted")
	}
	hours = 20
	good.Salary = nil
	if p.Match(good) != NeedsClarification {
		t.Fatal("missing pay must remain unknown")
	}
}

func TestWorldwideLocationDoesNotProveCountryEligibility(t *testing.T) {
	p := Preferences{Locations: []string{"Россия"}}
	if got := p.Match(Job{Location: "Worldwide"}); got != NeedsClarification {
		t.Fatalf("worldwide eligibility must remain unknown: %s", got)
	}
}
