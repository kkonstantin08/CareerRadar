package collector

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestHabrArchiveMarkerRequiresExplicitPageBanner(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{
		{`<div class="no-content__title">Вакансия в архиве</div>`, true},
		{`<div class="no-content__title"> Вакансия в архиве </div>`, true},
		{`<p>Эта вакансия в архиве другого сайта</p>`, false},
		{`<script>"Вакансия в архиве"</script>`, false},
		{`<div hidden><div class="no-content__title">Вакансия в архиве</div></div>`, false},
		{`<template><div class="no-content__title">Вакансия в архиве</div></template>`, false},
		{`<div aria-hidden="true"><div class="no-content__title">Вакансия в архиве</div></div>`, false},
		{`<div style="display: none"><div class="no-content__title">Вакансия в архиве</div></div>`, false},
		{`<div class="no-content__title">Вакансия в архиве другого сайта</div>`, false},
	} {
		if got := habrArchived([]byte(tc.body)); got != tc.want {
			t.Errorf("%q got %t want %t", tc.body, got, tc.want)
		}
	}
}

func TestHabrArchiveRequiresExactUnredirectedEvidence(t *testing.T) {
	const target = "https://career.habr.com/vacancies/1000061473"
	for _, tc := range []struct {
		status   int
		body     string
		redirect bool
		closed   bool
	}{
		{410, `<div class="no-content__title">Вакансия в архиве</div>`, false, true},
		{200, `<div class="no-content__title">Вакансия в архиве</div>`, false, true},
		{410, `Gone`, false, false},
		{410, `<script type="application/ld+json">{"@type":"JobPosting","title":"Stale"}</script>`, false, false},
		{403, `<div class="no-content__title">Вакансия в архиве</div>`, false, false},
		{410, `<div class="no-content__title">Вакансия в архиве</div>`, true, false},
	} {
		client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			if tc.redirect {
				return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://career.habr.com/vacancies/1000058777"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			}
			return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
		})}
		got, err := Fetch(context.Background(), client, target)
		closed := err == nil && len(got) == 1 && got[0].Closed && got[0].URL == target && got[0].SourceURL == target
		if closed != tc.closed {
			t.Errorf("status=%d redirect=%t jobs=%+v err=%v", tc.status, tc.redirect, got, err)
		}
	}
}
