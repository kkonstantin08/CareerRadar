package collector

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestX5InternshipStatusFromApplicationSection(t *testing.T) {
	const source = "https://generation.x5.ru/internships"
	const begin = `<main id="content"><section aria-label="Стажировка в Х5"><h1>Стажировка в Х5</h1></section><section aria-label="Подай заявку">`
	const closed = `<a href="/internships#registration">Набор закрыт</a>`
	const open = `<a href="#registration">Подать заявку</a>`
	const end = `</section><section id="registration"></section></main>`
	for _, tc := range []struct {
		name, body string
		status     int
		jobs       int
		closed     bool
		attention  bool
	}{
		{"closed", begin + closed + end, 200, 1, true, false},
		{"open", begin + open + end, 200, 1, false, false},
		{"both signals", begin + open + closed + end, 200, 1, false, true},
		{"hidden close", begin + `<div aria-hidden="true">` + closed + `</div>` + end, 200, 1, false, true},
		{"inline hidden close", begin + `<div style="display: none">` + closed + `</div>` + end, 200, 1, false, true},
		{"hidden utility close", begin + `<div class="hidden other">` + closed + `</div>` + end, 200, 1, false, true},
		{"invisible utility close", begin + `<div class="invisible">` + closed + `</div>` + end, 200, 1, false, true},
		{"other target", begin + strings.Replace(closed, "/internships", "/other", 1) + end, 200, 1, false, true},
		{"unrelated or embedded text", begin + `<script>Набор закрыт</script>` + end + closed, 200, 1, false, true},
		{"missing program", `<main id="content"><section aria-label="Подай заявку">` + closed + end, 200, 0, false, true},
		{"blocked", begin + closed + end, 403, 0, false, true},
		{"redirect", begin + closed + end, 302, 0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != source {
					t.Fatalf("unexpected request %s", r.URL)
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: http.Header{"Location": {"https://example.org/"}}, Request: r}, nil
			})}
			got, err := Fetch(context.Background(), client, source)
			if (err != nil) != tc.attention || len(got) != tc.jobs {
				t.Fatalf("jobs=%+v err=%v", got, err)
			}
			if len(got) == 0 {
				return
			}
			job := got[0]
			if job.Closed != tc.closed || job.URL != source || job.SourceURL != source {
				t.Fatalf("job=%+v", job)
			}
			if !tc.closed && (job.Title != "Стажировка в Х5" || job.Company != "X5" || job.Type != "Internship" || job.Level != "") {
				t.Fatalf("unexpected facts: %+v", job)
			}
			if tc.name == "open" && job.ApplyURL != source+"#registration" || tc.name != "open" && job.ApplyURL != "" {
				t.Fatalf("application link=%q", job.ApplyURL)
			}
		})
	}
}
