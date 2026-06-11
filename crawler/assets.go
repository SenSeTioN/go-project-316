package crawler

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

type Asset struct {
	URL        string `json:"url"`
	Type       string `json:"type"`
	StatusCode int    `json:"status_code"`
	SizeBytes  int64  `json:"size_bytes"`
	Error      string `json:"error"`
}

type assetRef struct {
	url     string
	assetTy string
}

// extractAssets обходит дерево HTML и собирает ресурсы страницы с их типом: <img> → image, <script src> → script, <link rel=stylesheet> → style. Дубликаты по URL в пределах страницы отбрасываются.
func extractAssets(root *html.Node, base *url.URL) []assetRef {
	seen := make(map[string]struct{})
	var refs []assetRef

	add := func(raw, ty string) {
		abs, ok := resolve(base, raw)
		if !ok {
			return
		}
		if _, dup := seen[abs]; dup {
			return
		}
		seen[abs] = struct{}{}
		refs = append(refs, assetRef{url: abs, assetTy: ty})
	}

	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "img":
				add(attrValue(n, "src"), "image")
			case "script":
				add(attrValue(n, "src"), "script")
			case "link":
				if strings.Contains(strings.ToLower(attrValue(n, "rel")), "stylesheet") {
					add(attrValue(n, "href"), "style")
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)

	return refs
}

func collectAssets(ctx context.Context, opts Options, lim *limiter, cache *resourceCache, refs []assetRef) []Asset {
	assets := []Asset{}
	for _, ref := range refs {
		if ctx.Err() != nil {
			break
		}
		assets = append(assets, buildAsset(ctx, opts, lim, cache, ref))
	}
	return assets
}

func buildAsset(ctx context.Context, opts Options, lim *limiter, cache *resourceCache, ref assetRef) Asset {
	r := getResource(ctx, opts, lim, cache, ref.url)

	asset := Asset{
		URL:        ref.url,
		Type:       ref.assetTy,
		StatusCode: r.statusCode,
		SizeBytes:  r.sizeBytes,
	}
	switch {
	case r.err != nil:
		asset.Error = r.err.Error()
	case r.statusCode >= 400:
		asset.Error = http.StatusText(r.statusCode)
	}
	return asset
}
