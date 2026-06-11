// Package crawler обходит сайт в пределах одного домена и формирует единый JSON-отчёт: для каждой страницы — SEO-теги, битые ссылки и размеры ассетов.
// Все HTTP-запросы идут через переданный в Options.HTTPClient клиент, поэтому обход полностью детерминирован и тестируется без реальной сети.
package crawler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"golang.org/x/net/html"
)

// Options управляет обходом. Обязателен только HTTPClient — через него идут все запросы. URL задаёт стартовую точку, Depth — лимит глубины, остальные поля настраивают повторы (Retries), темп (Delay/RPS), таймаут и User-Agent.
// IndentJSON включает форматирование отчёта с отступами.
type Options struct {
	URL         string
	Depth       int
	Retries     int
	Delay       time.Duration
	RPS         int
	Timeout     time.Duration
	UserAgent   string
	Concurrency int
	IndentJSON  bool
	HTTPClient  *http.Client
}

// Report — итоговый отчёт обхода: стартовый URL, лимит глубины, время генерациии список обойдённых страниц. Сериализуется в один JSON-документ.
type Report struct {
	RootURL     string `json:"root_url"`
	Depth       int    `json:"depth"`
	GeneratedAt string `json:"generated_at"`
	Pages       []Page `json:"pages"`
}

// Page — результат обработки одной страницы: URL и глубина, HTTP-статус, итог
// ("ok" или "error"), SEO-теги, найденные битые ссылки и ассеты. Все поля присутствуют в JSON всегда; пустые коллекции сериализуются как [].
type Page struct {
	URL          string       `json:"url"`
	Depth        int          `json:"depth"`
	HTTPStatus   int          `json:"http_status"`
	Status       string       `json:"status"`
	Error        string       `json:"error,omitempty"`
	SEO          SEO          `json:"seo"`
	BrokenLinks  []BrokenLink `json:"broken_links"`
	Assets       []Asset      `json:"assets"`
	DiscoveredAt string       `json:"discovered_at"`
}

// BrokenLink — недоступная ссылка со страницы: адрес, HTTP-код (0 при сетевой ошибке) и причина.
type BrokenLink struct {
	URL        string `json:"url"`
	StatusCode int    `json:"status_code"`
	Error      string `json:"error"`
}

// Analyze обходит сайт начиная с opts.URL и возвращает JSON-отчёт. Все запросы
// идут через opts.HTTPClient — он обязателен, иначе возвращается ошибка. Сетевые
// сбои отдельных страниц ошибкой Analyze не считаются: они попадают в отчёт со
// status="error". Ошибка возвращается только при отсутствии клиента или сбое
// сериализации.
func Analyze(ctx context.Context, opts Options) ([]byte, error) {
	if opts.HTTPClient == nil {
		return nil, errors.New("crawler: HTTPClient must be provided")
	}

	report := Report{
		RootURL:     opts.URL,
		Depth:       opts.Depth,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Pages:       crawl(ctx, opts),
	}

	if opts.IndentJSON {
		return json.MarshalIndent(report, "", "  ")
	}
	return json.Marshal(report)
}

// levelResult — результат обработки одной страницы уровня: сама страница и
// найденные на ней ссылки для следующего уровня глубины.
type levelResult struct {
	page  Page
	links []string
}

// crawl обходит сайт в ширину начиная со стартового URL. В очередь попадают только внутренние ссылки (тот же хост) с глубиной строго меньше opts.Depth:
// при depth=1 обрабатывается лишь стартовая страница. Каждый URL посещается один раз. Отмена контекста прерывает обход, собранные страницы сохраняются.
// Страницы одного уровня загружаются параллельно (до opts.Concurrency воркеров),
// при этом порядок отчёта остаётся детерминированным.
func crawl(ctx context.Context, opts Options) []Page {
	lim := newLimiter(rateInterval(opts))
	cache := newResourceCache()

	start, err := url.Parse(opts.URL)
	if err != nil {
		page, _ := fetch(ctx, opts, lim, cache, opts.URL, 0)
		return []Page{page}
	}

	workers := max(opts.Concurrency, 1)
	visited := map[string]struct{}{}
	visited[dedupKey(start)] = struct{}{}
	frontier := []string{displayURL(start)}
	pages := []Page{}

	for depth := 0; len(frontier) > 0; depth++ {
		if ctx.Err() != nil {
			break
		}

		results := fetchLevel(ctx, opts, lim, cache, frontier, depth, workers)

		var next []string
		for _, res := range results {
			if res == nil {
				continue
			}
			pages = append(pages, res.page)
			if depth+1 >= opts.Depth {
				continue
			}
			for _, link := range res.links {
				u, err := url.Parse(link)
				if err != nil || u.Host != start.Host {
					continue
				}
				key := dedupKey(u)
				if _, seen := visited[key]; seen {
					continue
				}
				visited[key] = struct{}{}
				next = append(next, link)
			}
		}
		sort.Strings(next)
		frontier = next
	}

	return pages
}

// fetchLevel загружает все URL одного уровня глубины, распределяя их между не более
// чем workers горутинами, и возвращает результаты в исходном порядке. Элемент
// остаётся nil, если обработка пропущена из-за отмены контекста. Общий limiter и
// кэш удерживают темп и дедуплицируют запросы на весь процесс.
func fetchLevel(ctx context.Context, opts Options, lim *limiter, cache *resourceCache, urls []string, depth, workers int) []*levelResult {
	results := make([]*levelResult, len(urls))
	jobs := make(chan int)

	var wg sync.WaitGroup
	worker := func() {
		defer wg.Done()
		for i := range jobs {
			if ctx.Err() != nil {
				continue
			}
			page, links := fetch(ctx, opts, lim, cache, urls[i], depth)
			results[i] = &levelResult{page: page, links: links}
		}
	}

	n := min(workers, len(urls))
	wg.Add(n)
	for range n {
		go worker()
	}
	for i := range urls {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	return results
}

// fetch загружает страницу pageURL и наполняет Page: статус, SEO, битые ссылки и
// ассеты. Вторым значением возвращает найденные на странице ссылки для дальнейшего
// обхода. При сетевой ошибке или статусе >= 400 разбор HTML не выполняется.
func fetch(ctx context.Context, opts Options, lim *limiter, cache *resourceCache, pageURL string, depth int) (Page, []string) {
	page := Page{
		URL:          pageURL,
		Depth:        depth,
		DiscoveredAt: time.Now().UTC().Format(time.RFC3339),
	}

	resp, err := doRequest(ctx, opts, lim, pageURL)
	if err != nil {
		page.Status = "error"
		page.Error = err.Error()
		return page, nil
	}
	defer func() { _ = resp.Body.Close() }()

	page.HTTPStatus = resp.StatusCode
	if resp.StatusCode >= http.StatusBadRequest {
		page.Status = "error"
		return page, nil
	}
	page.Status = "ok"

	root, err := html.Parse(resp.Body)
	if err != nil {
		return page, nil
	}

	page.SEO = extractSEO(root)

	base, err := url.Parse(pageURL)
	if err != nil {
		return page, nil
	}
	links := extractLinks(root, base)
	page.BrokenLinks = checkLinks(ctx, opts, lim, cache, links)
	page.Assets = collectAssets(ctx, opts, lim, cache, extractAssets(root, base))

	return page, links
}

// checkLinks запрашивает каждую ссылку через общий кэш и возвращает недоступные
// (статус >= 400 либо сетевая ошибка). Ссылки, оборвавшиеся из-за отмены
// контекста, битыми не считаются.
func checkLinks(ctx context.Context, opts Options, lim *limiter, cache *resourceCache, links []string) []BrokenLink {
	broken := []BrokenLink{}
	for _, link := range links {
		if ctx.Err() != nil {
			break
		}
		r := getResource(ctx, opts, lim, cache, link)
		switch {
		case r.err != nil:
			if isCancelErr(r.err) {
				continue
			}
			broken = append(broken, BrokenLink{URL: link, Error: r.err.Error()})
		case r.statusCode >= http.StatusBadRequest:
			broken = append(broken, BrokenLink{
				URL:        link,
				StatusCode: r.statusCode,
				Error:      http.StatusText(r.statusCode),
			})
		}
	}
	return broken
}

// setUserAgent проставляет заголовок User-Agent, если он задан.
func setUserAgent(req *http.Request, ua string) {
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
}
