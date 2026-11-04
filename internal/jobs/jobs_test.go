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

func TestGeographyMatchesCommonEnglishAndRussianNames(t *testing.T) {
	for _, tc := range []struct{ preference, location string }{
		{"Россия", "Russia"}, {"Россия", "RU"}, {"Казань", "Kazan"}, {"Иннополис", "Innopolis"},
	} {
		p := Preferences{Locations: []string{tc.preference}}
		if got := p.Match(Job{Location: tc.location}); got != Suitable {
			t.Errorf("%s / %s: %s", tc.preference, tc.location, got)
		}
	}
}

func TestCountryOnlyDoesNotDisproveSpecificCity(t *testing.T) {
	for _, city := range []string{"Казань", "Москва", "Иннополис"} {
		p := Preferences{Locations: []string{city}}
		if got := p.Match(Job{Location: "RU"}); got != NeedsClarification {
			t.Fatalf("country does not specify %s: %s", city, got)
		}
	}
}

func TestUnspecifiedLevelCanBeSelected(t *testing.T) {
	p := Preferences{Levels: []string{"Не указан"}}
	if got := p.Match(Job{}); got != NeedsClarification {
		t.Fatalf("unspecified selected: %s", got)
	}
	if got := p.Match(Job{Level: "Junior"}); got != Unsuitable {
		t.Fatalf("known level not selected: %s", got)
	}
	p.Levels = []string{"Junior"}
	if got := p.Match(Job{}); got != NeedsClarification {
		t.Fatalf("unknown must remain visible: %s", got)
	}
}

func TestSalaryRangeMatchesWithoutGuessing(t *testing.T) {
	minimum := int64(100000)
	p := Preferences{MinSalary: &minimum, Currency: "RUB"}
	for _, tc := range []struct {
		low, high *int64
		want      Match
	}{
		{ptrSalary(110000), ptrSalary(150000), Suitable},
		{ptrSalary(50000), ptrSalary(90000), Unsuitable},
		{ptrSalary(90000), ptrSalary(120000), NeedsClarification},
		{nil, ptrSalary(90000), Unsuitable},
		{ptrSalary(110000), nil, Suitable},
		{ptrSalary(120000), ptrSalary(80000), NeedsClarification},
		{ptrSalary(100000), ptrSalary(100000), Suitable},
	} {
		if got := p.Match(Job{SalaryMin: tc.low, SalaryMax: tc.high, Currency: "RUB"}); got != tc.want {
			t.Errorf("range %v..%v: %s", tc.low, tc.high, got)
		}
	}
	if got := p.Match(Job{SalaryMin: ptrSalary(110000), SalaryMax: ptrSalary(150000), Currency: "USD"}); got != NeedsClarification {
		t.Fatalf("currency mismatch: %s", got)
	}
}

func ptrSalary(n int64) *int64 { return &n }

func TestMatchMultiplePublishedLanguages(t *testing.T) {
	p := Preferences{Languages: []string{"Python"}}
	if got := p.Match(Job{Language: "Go, Python"}); got != Suitable {
		t.Fatalf("published Python should match: %s", got)
	}
	if got := p.Match(Job{Language: "Go, Java"}); got != Unsuitable {
		t.Fatalf("published languages do not match: %s", got)
	}
}
