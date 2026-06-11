package crawler

import (
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// linkAttrs сопоставляет HTML-тег с атрибутом, в котором лежит ссылка.
var linkAttrs = map[string]string{
	"a": "href",
}

// extractLinks обходит дерево HTML и возвращает уникальные абсолютные
// http(s)-ссылки, разрешённые относительно base. Пустые значения, якоря и
// неподдерживаемые схемы (mailto, tel, javascript, data и т.п.) отбрасываются.
func extractLinks(root *html.Node, base *url.URL) []string {
	seen := make(map[string]struct{})
	var links []string

	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if attr, ok := linkAttrs[n.Data]; ok {
				if raw := attrValue(n, attr); raw != "" {
					if abs, ok := resolve(base, raw); ok {
						if _, dup := seen[abs]; !dup {
							seen[abs] = struct{}{}
							links = append(links, abs)
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)

	return links
}

// attrValue возвращает обрезанное от пробелов значение атрибута key узла или "".
func attrValue(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}

// resolve разрешает raw относительно base и нормализует результат. Возвращает
// false для пустых значений, якорей и не-http(s) схем (mailto, tel, data и т.п.).
func resolve(base *url.URL, raw string) (string, bool) {
	if raw == "" || strings.HasPrefix(raw, "#") {
		return "", false
	}

	ref, err := url.Parse(raw)
	if err != nil {
		return "", false
	}

	abs := base.ResolveReference(ref)
	if abs.Scheme != "http" && abs.Scheme != "https" {
		return "", false
	}

	return displayURL(abs), true
}

// displayURL — URL в том виде, в каком он попадает в отчёт: убран только фрагмент,
// путь не трогаем (http://host остаётся без хвостового "/").
func displayURL(u *url.URL) string {
	v := *u
	v.Fragment = ""
	return v.String()
}

// dedupKey — ключ дедупликации страниц: фрагмент убран, пустой путь считается
// "/", чтобы http://host и http://host/ были одной и той же страницей.
func dedupKey(u *url.URL) string {
	v := *u
	v.Fragment = ""
	if v.Path == "" {
		v.Path = "/"
	}
	return v.String()
}
