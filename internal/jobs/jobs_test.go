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
