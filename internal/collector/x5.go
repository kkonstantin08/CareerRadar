package collector

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"

	"golang.org/x/net/html"
)

const x5InternshipURL = "https://generation.x5.ru/internships"

// X5InternshipEvidenceURL identifies the official program page used as registration evidence.
func X5InternshipEvidenceURL(raw string) string {
	if raw == x5InternshipURL {
		return raw
	}
	return ""
}

func fetchX5Internship(ctx context.Context, client *http.Client) ([]Job, error) {
	direct := *client
	direct.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	body, actual, err := readPage(ctx, &direct, x5InternshipURL)
	if err != nil {
		return nil, err
	}
	if actual != x5InternshipURL {
		return nil, errors.New("unexpected X5 program URL; source requires attention")
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var program, registration, open, closed bool
	for node := range doc.Descendants() {
		if node.Type != html.ElementNode {
			continue
		}
		if node.Data == "section" {
			if label := attribute(node, "aria-label"); label == "Стажировка в Х5" && x5ProgramSection(node) == node {
				program = true
			}
			if attribute(node, "id") == "registration" && x5ProgramSection(node) == node {
				registration = true
			}
			continue
		}
		if node.Data != "a" {
			continue
		}
		ref := attribute(node, "href")
		if ref != "#registration" && ref != "/internships#registration" && ref != x5InternshipURL+"#registration" {
			continue
		}
		section := x5ProgramSection(node)
		if section == nil || attribute(section, "aria-label") != "Подай заявку" {
			continue
		}
		// The application section renders its current state as the link's direct text.
		text := node.FirstChild
		if text == nil || text != node.LastChild || text.Type != html.TextNode {
			continue
		}
		switch strings.Join(strings.Fields(text.Data), " ") {
		case "Набор закрыт":
			closed = true
		case "Подать заявку":
			open = true
		}
	}
	if !program {
		return nil, errors.New("X5 program section could not be verified; source requires attention")
	}
	job := Job{Title: "Стажировка в Х5", Company: "X5", Type: "Internship", URL: x5InternshipURL, SourceURL: x5InternshipURL}
	if !registration || open == closed {
		return []Job{job}, errors.New("X5 registration status requires clarification")
	}
	if closed {
		return []Job{{Closed: true, URL: x5InternshipURL, SourceURL: x5InternshipURL}}, nil
	}
	job.ApplyURL = x5InternshipURL + "#registration"
	return []Job{job}, nil
}

// x5ProgramSection restricts state markers to visible sections of the program's main content.
func x5ProgramSection(n *html.Node) *html.Node {
	var section *html.Node
	var inMain bool
	for ; n != nil; n = n.Parent {
		if n.Type != html.ElementNode {
			continue
		}
		classes := strings.Fields(attribute(n, "class"))
		if hiddenHTMLElement(n) || contains(classes, "hidden") || contains(classes, "invisible") {
			return nil
		}
		if section == nil && n.Data == "section" {
			section = n
		}
		inMain = inMain || n.Data == "main" && attribute(n, "id") == "content"
	}
	if !inMain {
		return nil
	}
	return section
}
