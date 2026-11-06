package discovery

import (
	"context"
	"net/http"
	"sync"

	"github.com/kkonstantin08/CareerRadar/internal/collector"
)

func enrichCards(ctx context.Context, client *http.Client, results []Result) {
	tasks := make(chan int, len(results))
	for i := range results {
		tasks <- i
	}
	close(tasks)
	var workers sync.WaitGroup
	for range min(2, len(results)) {
		workers.Go(func() {
			for i := range tasks {
				r := &results[i]
				if err := ctx.Err(); err != nil {
					r.Problem = "Карточка требует внимания: " + err.Error()
					continue
				}
				found, err := collector.Fetch(ctx, client, r.URL)
				if err != nil {
					r.Problem = "Карточка требует внимания: " + err.Error()
				}
				if len(found) != 1 || found[0].Closed || VacancyURL(found[0].URL) != r.URL || VacancyURL(found[0].SourceURL) != r.URL {
					if r.Problem == "" {
						r.Problem = "Не удалось однозначно сопоставить карточку вакансии"
					}
					continue
				}
				r.Details = &found[0]
				if found[0].Location != "" || found[0].LocationUncertain {
					r.Location = found[0].Location
				}
				if found[0].WorkMode != "" {
					r.WorkMode = found[0].WorkMode
				}
				if r.Problem == "" && (found[0].LocationUncertain || found[0].Title == "" || found[0].Company == "" || found[0].Deadline == "") {
					r.Problem = "Карточка содержит неполные или неоднозначные сведения"
				}
			}
		})
	}
	workers.Wait()
}
