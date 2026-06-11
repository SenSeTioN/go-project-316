package crawler

import (
	"strings"

	"golang.org/x/net/html"
)

// SEO — базовые SEO-показатели страницы: наличие и текст <title> и meta
// description, а также факт наличия <h1>.
type SEO struct {
	HasTitle       bool   `json:"has_title"`
	Title          string `json:"title"`
	HasDescription bool   `json:"has_description"`
	Description    string `json:"description"`
	HasH1          bool   `json:"has_h1"`
}

// extractSEO обходит дерево HTML и собирает базовые SEO-показатели: первый
// <title>, <meta name="description"> и факт наличия <h1>. HTML-сущности уже
// раскодированы парсером, остаётся только обрезать пробелы.
func extractSEO(root *html.Node) SEO {
	var seo SEO

	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "title":
				if !seo.HasTitle {
					seo.HasTitle = true
					seo.Title = strings.TrimSpace(textContent(n))
				}
			case "meta":
				if !seo.HasDescription && strings.EqualFold(attrValue(n, "name"), "description") {
					seo.HasDescription = true
					seo.Description = strings.TrimSpace(attrValue(n, "content"))
				}
			case "h1":
				seo.HasH1 = true
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)

	return seo
}

// textContent собирает текст узла, рекурсивно склеивая содержимое вложенных
// элементов.
func textContent(n *html.Node) string {
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			sb.WriteString(c.Data)
		case html.ElementNode:
			sb.WriteString(textContent(c))
		}
	}
	return sb.String()
}
