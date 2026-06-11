package crawler_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"code/crawler"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func decode(t *testing.T, data []byte) crawler.Report {
	t.Helper()
	var report crawler.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(report.Pages) != 1 {
		t.Fatalf("len(pages) = %d, want 1", len(report.Pages))
	}
	return report
}

func TestAnalyzeSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	data, err := crawler.Analyze(context.Background(), crawler.Options{
		URL:        srv.URL,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}

	report := decode(t, data)
	if report.RootURL != srv.URL {
		t.Errorf("root_url = %q, want %q", report.RootURL, srv.URL)
	}
	page := report.Pages[0]
	if page.HTTPStatus != http.StatusOK {
		t.Errorf("http_status = %d, want 200", page.HTTPStatus)
	}
	if page.Status != "ok" {
		t.Errorf("status = %q, want %q", page.Status, "ok")
	}
	if page.Depth != 0 {
		t.Errorf("depth = %d, want 0", page.Depth)
	}
	if page.Error != "" {
		t.Errorf("error = %q, want empty", page.Error)
	}
}

func TestAnalyzeHTTPErrorStatus(t *testing.T) {
	cases := []int{http.StatusNotFound, http.StatusInternalServerError}

	for _, code := range cases {
		t.Run(http.StatusText(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
			}))
			defer srv.Close()

			data, err := crawler.Analyze(context.Background(), crawler.Options{
				URL:        srv.URL,
				HTTPClient: srv.Client(),
			})
			if err != nil {
				t.Fatalf("Analyze must not return error on HTTP %d, got: %v", code, err)
			}

			page := decode(t, data).Pages[0]
			if page.HTTPStatus != code {
				t.Errorf("http_status = %d, want %d", page.HTTPStatus, code)
			}
			if page.Status != "error" {
				t.Errorf("status = %q, want %q", page.Status, "error")
			}
		})
	}
}

func TestAnalyzeTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer srv.Close()

	client := srv.Client()
	client.Timeout = 10 * time.Millisecond

	data, err := crawler.Analyze(context.Background(), crawler.Options{
		URL:        srv.URL,
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("Analyze must not return error on timeout, got: %v", err)
	}

	page := decode(t, data).Pages[0]
	if page.HTTPStatus != 0 {
		t.Errorf("http_status = %d, want 0", page.HTTPStatus)
	}
	if page.Status != "error" {
		t.Errorf("status = %q, want %q", page.Status, "error")
	}
	if page.Error == "" {
		t.Error("error must be non-empty on timeout")
	}
}

func TestAnalyzeNetworkError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})}

	data, err := crawler.Analyze(context.Background(), crawler.Options{
		URL:        "https://example.com",
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("Analyze must not return error on network failure, got: %v", err)
	}

	page := decode(t, data).Pages[0]
	if page.HTTPStatus != 0 {
		t.Errorf("http_status = %d, want 0", page.HTTPStatus)
	}
	if page.Status != "error" {
		t.Errorf("status = %q, want %q", page.Status, "error")
	}
	if page.Error == "" {
		t.Error("error must be non-empty on network failure")
	}
}

func TestAnalyzeNilClient(t *testing.T) {
	_, err := crawler.Analyze(context.Background(), crawler.Options{URL: "https://example.com"})
	if err == nil {
		t.Fatal("Analyze must return error when HTTPClient is nil")
	}
}

func serveHTML(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func analyzePage(t *testing.T, srv *httptest.Server) crawler.Page {
	t.Helper()
	data, err := crawler.Analyze(context.Background(), crawler.Options{
		URL:        srv.URL,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	return decode(t, data).Pages[0]
}

func TestAnalyzeSEOAllTags(t *testing.T) {
	srv := serveHTML(t, `<html>
		<head>
			<title>Example Test</title>
			<meta name="description" content="A short description">
		</head>
		<body><h1>Main heading</h1></body>
	</html>`)

	seo := analyzePage(t, srv).SEO
	if !seo.HasTitle || seo.Title != "Example Test" {
		t.Errorf("title: has=%v value=%q", seo.HasTitle, seo.Title)
	}
	if !seo.HasDescription || seo.Description != "A short description" {
		t.Errorf("description: has=%v value=%q", seo.HasDescription, seo.Description)
	}
	if !seo.HasH1 {
		t.Error("has_h1 = false, want true")
	}
}

func TestAnalyzeSEOMissingTags(t *testing.T) {
	srv := serveHTML(t, `<html><head></head><body><p>no seo here</p></body></html>`)

	seo := analyzePage(t, srv).SEO
	if seo.HasTitle || seo.Title != "" {
		t.Errorf("title: has=%v value=%q, want false/empty", seo.HasTitle, seo.Title)
	}
	if seo.HasDescription || seo.Description != "" {
		t.Errorf("description: has=%v value=%q, want false/empty", seo.HasDescription, seo.Description)
	}
	if seo.HasH1 {
		t.Error("has_h1 = true, want false")
	}
}

func TestAnalyzeSEOHTMLEntities(t *testing.T) {
	srv := serveHTML(t, `<html>
		<head>
			<title>Tom &amp; Jerry</title>
			<meta name="description" content="Cats &amp; dogs">
		</head>
		<body><h1>x</h1></body>
	</html>`)

	seo := analyzePage(t, srv).SEO
	if seo.Title != "Tom & Jerry" {
		t.Errorf("title = %q, want %q", seo.Title, "Tom & Jerry")
	}
	if seo.Description != "Cats & dogs" {
		t.Errorf("description = %q, want %q", seo.Description, "Cats & dogs")
	}
}

func pageURLs(pages []crawler.Page) map[string]int {
	counts := make(map[string]int)
	for _, p := range pages {
		counts[p.URL]++
	}
	return counts
}

func analyzeReport(t *testing.T, srv *httptest.Server, depth int) crawler.Report {
	t.Helper()
	data, err := crawler.Analyze(context.Background(), crawler.Options{
		URL:        srv.URL,
		Depth:      depth,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	var report crawler.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	return report
}

func TestAnalyzeDepthLimit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<a href="/a">a</a><a href="/b">b</a>`)
	})
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<a href="/c">c</a>`)
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/c", func(w http.ResponseWriter, r *http.Request) {})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cases := []struct {
		depth     int
		wantPages int
	}{
		{depth: 1, wantPages: 1}, // только стартовая
		{depth: 2, wantPages: 3}, // /, /a, /b
		{depth: 3, wantPages: 4}, // + /c
	}
	for _, tc := range cases {
		report := analyzeReport(t, srv, tc.depth)
		if len(report.Pages) != tc.wantPages {
			t.Errorf("depth=%d: got %d pages, want %d (%v)",
				tc.depth, len(report.Pages), tc.wantPages, pageURLs(report.Pages))
		}
	}
}

func TestAnalyzeExternalNotCrawled(t *testing.T) {
	ext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ext.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<a href="/in1">in1</a><a href="/in2">in2</a>`+
			`<a href="`+ext.URL+`/external">ext</a>`)
	})
	mux.HandleFunc("/in1", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/in2", func(w http.ResponseWriter, r *http.Request) {})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	report := analyzeReport(t, srv, 2)

	if len(report.Pages) != 3 {
		t.Errorf("got %d pages, want 3 (%v)", len(report.Pages), pageURLs(report.Pages))
	}
	for _, p := range report.Pages {
		if strings.Contains(p.URL, ext.Listener.Addr().String()) {
			t.Errorf("external page must not be crawled: %s", p.URL)
		}
	}
}

func TestAnalyzeDuplicateLinks(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<a href="/dup">1</a><a href="/dup">2</a><a href="/dup">3</a>`)
	})
	mux.HandleFunc("/dup", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<a href="/">home</a>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	report := analyzeReport(t, srv, 5)

	counts := pageURLs(report.Pages)
	if counts[srv.URL+"/dup"] != 1 {
		t.Errorf("/dup appears %d times, want 1 (%v)", counts[srv.URL+"/dup"], counts)
	}
	if len(report.Pages) != 2 {
		t.Errorf("got %d pages, want 2 (%v)", len(report.Pages), counts)
	}
}

func TestAnalyzeContextCancelled(t *testing.T) {
	srv := serveHTML(t, `<a href="/x">x</a>`)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	data, err := crawler.Analyze(ctx, crawler.Options{
		URL:        srv.URL,
		Depth:      10,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("Analyze must return valid report on cancel, got error: %v", err)
	}
	var report crawler.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("report must be valid JSON after cancel: %v", err)
	}
}

// infiniteSite отдаёт на любой путь страницу со ссылкой на следующий уровень
// в глубину, так что обход упирается в лимит времени, а не в конец сайта.
func infiniteSite(t *testing.T, counter *int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(counter, 1)
		_, _ = io.WriteString(w, `<a href="`+r.URL.Path+`a">next</a>`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAnalyzeRateLimit(t *testing.T) {
	var count int64
	srv := infiniteSite(t, &count)

	const (
		interval = 25 * time.Millisecond
		period   = 200 * time.Millisecond
	)
	ctx, cancel := context.WithTimeout(context.Background(), period)
	defer cancel()

	data, err := crawler.Analyze(ctx, crawler.Options{
		URL:        srv.URL,
		Depth:      1000,
		Delay:      interval,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if !json.Valid(data) {
		t.Fatal("report is not valid JSON")
	}

	total := atomic.LoadInt64(&count)
	maxExpected := int64(period/interval) + 2 // 8 + запас
	if total > maxExpected {
		t.Errorf("rate limit exceeded: %d requests in %v, want <= %d", total, period, maxExpected)
	}
	if total == 0 {
		t.Error("no requests performed")
	}
}

func TestAnalyzeRateLimitCancelNoHang(t *testing.T) {
	srv := serveHTML(t, `<a href="/x">x</a>`)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	data, err := crawler.Analyze(ctx, crawler.Options{
		URL:        srv.URL,
		Depth:      10,
		Delay:      10 * time.Second, // огромная задержка: без отмены тест завис бы
		HTTPClient: srv.Client(),
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if !json.Valid(data) {
		t.Fatal("report is not valid JSON")
	}
	if elapsed > 2*time.Second {
		t.Errorf("rate limit wait did not cancel promptly: %v", elapsed)
	}
}

func TestRetrySucceedsAfterTemporary(t *testing.T) {
	var n int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&n, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable) // 503 — временная
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "<html></html>")
	}))
	defer srv.Close()

	data, err := crawler.Analyze(context.Background(), crawler.Options{
		URL:        srv.URL,
		Depth:      1,
		Retries:    2,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}

	page := decode(t, data).Pages[0]
	if page.Status != "ok" || page.HTTPStatus != http.StatusOK {
		t.Errorf("status=%q http_status=%d, want ok/200", page.Status, page.HTTPStatus)
	}
	if got := atomic.LoadInt64(&n); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}
}

func TestRetryExhausted(t *testing.T) {
	var n int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&n, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	const retries = 2
	data, err := crawler.Analyze(context.Background(), crawler.Options{
		URL:        srv.URL,
		Depth:      1,
		Retries:    retries,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}

	page := decode(t, data).Pages[0]
	if page.Status != "error" || page.HTTPStatus != http.StatusInternalServerError {
		t.Errorf("status=%q http_status=%d, want error/500", page.Status, page.HTTPStatus)
	}
	if got := atomic.LoadInt64(&n); got != retries+1 {
		t.Errorf("requests = %d, want %d (retries+1)", got, retries+1)
	}
}

func TestRetryBrokenLinkLastAttempt(t *testing.T) {
	var flaky int64
	mux := http.NewServeMux()
	mux.HandleFunc("/flaky", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&flaky, 1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<a href="/flaky">flaky</a>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	const retries = 1
	data, err := crawler.Analyze(context.Background(), crawler.Options{
		URL:        srv.URL,
		Depth:      1,
		Retries:    retries,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}

	page := decode(t, data).Pages[0]
	if len(page.BrokenLinks) != 1 {
		t.Fatalf("broken_links = %d, want 1: %+v", len(page.BrokenLinks), page.BrokenLinks)
	}
	if page.BrokenLinks[0].StatusCode != http.StatusInternalServerError {
		t.Errorf("broken status_code = %d, want 500", page.BrokenLinks[0].StatusCode)
	}
	if got := atomic.LoadInt64(&flaky); got != retries+1 {
		t.Errorf("flaky requests = %d, want %d", got, retries+1)
	}
}

func TestRetryCancelStops(t *testing.T) {
	var n int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&n, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	data, err := crawler.Analyze(ctx, crawler.Options{
		URL:        srv.URL,
		Depth:      1,
		Retries:    10, // без отмены было бы 11 попыток с растущими паузами
		HTTPClient: srv.Client(),
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if !json.Valid(data) {
		t.Fatal("report is not valid JSON")
	}
	if elapsed > 2*time.Second {
		t.Errorf("cancel did not stop retries promptly: %v", elapsed)
	}
	if got := atomic.LoadInt64(&n); got > 2 {
		t.Errorf("requests after cancel = %d, want few", got)
	}
}

func findAsset(assets []crawler.Asset, url string) (crawler.Asset, bool) {
	for _, a := range assets {
		if strings.HasSuffix(a.URL, url) {
			return a, true
		}
	}
	return crawler.Asset{}, false
}

func TestAssetsTypesAndSizes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/a.png", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "PNG") })
	mux.HandleFunc("/a.js", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "JSJS") })
	mux.HandleFunc("/a.css", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "CSSCS") })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<img src="/a.png"><script src="/a.js"></script>`+
			`<link rel="stylesheet" href="/a.css">`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	page := analyzePage(t, srv)
	if len(page.Assets) != 3 {
		t.Fatalf("assets = %d, want 3: %+v", len(page.Assets), page.Assets)
	}

	want := map[string]struct {
		ty   string
		size int64
	}{
		"/a.png": {"image", 3},
		"/a.js":  {"script", 4},
		"/a.css": {"style", 5},
	}
	for path, exp := range want {
		a, ok := findAsset(page.Assets, path)
		if !ok {
			t.Errorf("asset %s not found", path)
			continue
		}
		if a.Type != exp.ty {
			t.Errorf("%s type = %q, want %q", path, a.Type, exp.ty)
		}
		if a.SizeBytes != exp.size {
			t.Errorf("%s size = %d, want %d", path, a.SizeBytes, exp.size)
		}
		if a.StatusCode != http.StatusOK || a.Error != "" {
			t.Errorf("%s status=%d error=%q, want 200/empty", path, a.StatusCode, a.Error)
		}
	}
}

func TestAssetMissingContentLength(t *testing.T) {
	body := "no-content-length-body"
	mux := http.NewServeMux()
	mux.HandleFunc("/a.png", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush() // фиксируем заголовки без Content-Length → chunked
		_, _ = io.WriteString(w, body)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<img src="/a.png">`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	page := analyzePage(t, srv)
	a, ok := findAsset(page.Assets, "/a.png")
	if !ok {
		t.Fatal("asset not found")
	}
	if a.SizeBytes != int64(len(body)) {
		t.Errorf("size = %d, want %d (measured from body)", a.SizeBytes, len(body))
	}
	if a.Error != "" {
		t.Errorf("error = %q, want empty", a.Error)
	}
}

func TestAssetCachedSingleRequest(t *testing.T) {
	var assetHits int64
	mux := http.NewServeMux()
	mux.HandleFunc("/shared.png", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&assetHits, 1)
		_, _ = io.WriteString(w, "IMG")
	})
	mux.HandleFunc("/p1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<img src="/shared.png">`)
	})
	mux.HandleFunc("/p2", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<img src="/shared.png">`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<a href="/p1">1</a><a href="/p2">2</a>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	report := analyzeReport(t, srv, 2)

	// ассет встречается на /p1 и /p2, но запрашивается один раз
	if got := atomic.LoadInt64(&assetHits); got != 1 {
		t.Errorf("asset transport hits = %d, want 1", got)
	}

	// данные ассета на обеих страницах совпадают
	var seen *crawler.Asset
	for i := range report.Pages {
		if a, ok := findAsset(report.Pages[i].Assets, "/shared.png"); ok {
			if seen != nil && (*seen != a) {
				t.Errorf("asset data differs across pages: %+v vs %+v", *seen, a)
			}
			a := a
			seen = &a
		}
	}
	if seen == nil {
		t.Fatal("shared asset not present in any page")
	}
}

func TestAssetErrorStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/missing.png", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<img src="/missing.png">`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	data, err := crawler.Analyze(context.Background(), crawler.Options{
		URL:        srv.URL,
		Depth:      1,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}

	page := decode(t, data).Pages[0]
	a, ok := findAsset(page.Assets, "/missing.png")
	if !ok {
		t.Fatal("asset not found")
	}
	if a.StatusCode != http.StatusNotFound {
		t.Errorf("status_code = %d, want 404", a.StatusCode)
	}
	if a.Error == "" {
		t.Error("error must be non-empty for 404 asset")
	}

	// все поля присутствуют в JSON даже при ошибке
	var raw struct {
		Pages []struct {
			Assets []map[string]json.RawMessage `json:"assets"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	obj := raw.Pages[0].Assets[0]
	for _, key := range []string{"url", "type", "status_code", "size_bytes", "error"} {
		if _, present := obj[key]; !present {
			t.Errorf("asset JSON missing key %q", key)
		}
	}
}

func referenceSite(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/missing", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/static/logo.png", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 12345)) // тело ровно 12345 байт → Content-Length
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<html><head>
			<title>Example title</title>
			<meta name="description" content="Example description">
		</head><body>
			<h1>Heading</h1>
			<a href="/missing">missing</a>
			<img src="/static/logo.png">
		</body></html>`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestReportMatchesReference(t *testing.T) {
	srv := referenceSite(t)

	data, err := crawler.Analyze(context.Background(), crawler.Options{
		URL:        srv.URL,
		Depth:      1,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}

	// значения
	report := decode(t, data)
	if report.RootURL != srv.URL || report.Depth != 1 {
		t.Errorf("root_url=%q depth=%d", report.RootURL, report.Depth)
	}
	if _, err := time.Parse(time.RFC3339, report.GeneratedAt); err != nil {
		t.Errorf("generated_at not ISO8601: %v", err)
	}

	page := report.Pages[0]
	if page.HTTPStatus != 200 || page.Status != "ok" || page.Error != "" {
		t.Errorf("page http=%d status=%q error=%q", page.HTTPStatus, page.Status, page.Error)
	}
	if _, err := time.Parse(time.RFC3339, page.DiscoveredAt); err != nil {
		t.Errorf("discovered_at not ISO8601: %v", err)
	}

	wantSEO := crawler.SEO{
		HasTitle: true, Title: "Example title",
		HasDescription: true, Description: "Example description",
		HasH1: true,
	}
	if page.SEO != wantSEO {
		t.Errorf("seo = %+v, want %+v", page.SEO, wantSEO)
	}

	if len(page.BrokenLinks) != 1 {
		t.Fatalf("broken_links = %+v", page.BrokenLinks)
	}
	bl := page.BrokenLinks[0]
	if !strings.HasSuffix(bl.URL, "/missing") || bl.StatusCode != 404 || bl.Error != "Not Found" {
		t.Errorf("broken_link = %+v", bl)
	}

	if len(page.Assets) != 1 {
		t.Fatalf("assets = %+v", page.Assets)
	}
	as := page.Assets[0]
	if !strings.HasSuffix(as.URL, "/static/logo.png") || as.Type != "image" ||
		as.StatusCode != 200 || as.SizeBytes != 12345 || as.Error != "" {
		t.Errorf("asset = %+v", as)
	}

	// все обязательные ключи присутствуют (по структуре эталона)
	assertKeys(t, data)
}

func assertKeys(t *testing.T, data []byte) {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"root_url", "depth", "generated_at", "pages"} {
		if _, ok := top[k]; !ok {
			t.Errorf("top-level key %q missing", k)
		}
	}

	var pages []map[string]json.RawMessage
	_ = json.Unmarshal(top["pages"], &pages)
	page := pages[0]
	for _, k := range []string{"url", "depth", "http_status", "status", "seo", "broken_links", "assets", "discovered_at"} {
		if _, ok := page[k]; !ok {
			t.Errorf("page key %q missing", k)
		}
	}

	var seo map[string]json.RawMessage
	_ = json.Unmarshal(page["seo"], &seo)
	for _, k := range []string{"has_title", "title", "has_description", "description", "has_h1"} {
		if _, ok := seo[k]; !ok {
			t.Errorf("seo key %q missing", k)
		}
	}

	var bls []map[string]json.RawMessage
	_ = json.Unmarshal(page["broken_links"], &bls)
	for _, k := range []string{"url", "status_code", "error"} {
		if _, ok := bls[0][k]; !ok {
			t.Errorf("broken_link key %q missing", k)
		}
	}

	var assets []map[string]json.RawMessage
	_ = json.Unmarshal(page["assets"], &assets)
	for _, k := range []string{"url", "type", "status_code", "size_bytes"} {
		if _, ok := assets[0][k]; !ok {
			t.Errorf("asset key %q missing", k)
		}
	}
}

func TestIndentJSONSameContent(t *testing.T) {
	srv := referenceSite(t)

	compact, err := crawler.Analyze(context.Background(), crawler.Options{
		URL: srv.URL, Depth: 1, IndentJSON: false, HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	indented, err := crawler.Analyze(context.Background(), crawler.Options{
		URL: srv.URL, Depth: 1, IndentJSON: true, HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// форматирование различается
	if !strings.Contains(string(indented), "\n  ") {
		t.Error("indented output must contain newlines and indentation")
	}
	if strings.Contains(string(compact), "\n") {
		t.Error("compact output must not contain newlines")
	}

	// содержание идентично (с поправкой на динамические времена)
	r1 := decode(t, compact)
	r2 := decode(t, indented)
	normalizeReport(&r1)
	normalizeReport(&r2)
	if !reflect.DeepEqual(r1, r2) {
		t.Errorf("content differs between compact and indented:\n%+v\n%+v", r1, r2)
	}
}

func normalizeReport(r *crawler.Report) {
	r.GeneratedAt = ""
	for i := range r.Pages {
		r.Pages[i].DiscoveredAt = ""
	}
}

func TestAnalyzeBrokenLinks(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/good", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/bad", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<html><body>
			<a href="/good">good</a>
			<a href="/bad">bad</a>
			<a href="mailto:test@example.com">mail</a>
			<a href="#section">anchor</a>
			<a href="">empty</a>
			<a href="javascript:void(0)">js</a>
		</body></html>`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	data, err := crawler.Analyze(context.Background(), crawler.Options{
		URL:        srv.URL,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}

	page := decode(t, data).Pages[0]
	if page.Status != "ok" {
		t.Fatalf("page status = %q, want %q", page.Status, "ok")
	}
	if len(page.BrokenLinks) != 1 {
		t.Fatalf("len(broken_links) = %d, want 1: %+v", len(page.BrokenLinks), page.BrokenLinks)
	}

	broken := page.BrokenLinks[0]
	if broken.URL != srv.URL+"/bad" {
		t.Errorf("broken url = %q, want %q", broken.URL, srv.URL+"/bad")
	}
	if broken.StatusCode != http.StatusNotFound {
		t.Errorf("broken status_code = %d, want 404", broken.StatusCode)
	}
	if broken.Error != "Not Found" {
		t.Errorf("broken error = %q, want %q", broken.Error, "Not Found")
	}
}

func TestAnalyzeConcurrentWorkers(t *testing.T) {
	var inFlight, maxInFlight int64
	mark := func() {
		cur := atomic.AddInt64(&inFlight, 1)
		for {
			m := atomic.LoadInt64(&maxInFlight)
			if cur <= m || atomic.CompareAndSwapInt64(&maxInFlight, m, cur) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		atomic.AddInt64(&inFlight, -1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<a href="/a">a</a><a href="/b">b</a><a href="/c">c</a><a href="/d">d</a>`)
	})
	for _, p := range []string{"/a", "/b", "/c", "/d"} {
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) { mark() })
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	data, err := crawler.Analyze(context.Background(), crawler.Options{
		URL:         srv.URL,
		Depth:       2,
		Concurrency: 4,
		HTTPClient:  srv.Client(),
	})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}

	var report crawler.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(report.Pages) != 5 {
		t.Errorf("got %d pages, want 5 (%v)", len(report.Pages), pageURLs(report.Pages))
	}
	if got := atomic.LoadInt64(&maxInFlight); got < 2 {
		t.Errorf("max concurrent requests = %d, want >= 2 (workers do not parallelize)", got)
	}
}

func TestAnalyzeConcurrentSharedAssetSingleRequest(t *testing.T) {
	var hits int64
	mux := http.NewServeMux()
	mux.HandleFunc("/shared.png", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		time.Sleep(20 * time.Millisecond) // окно, в котором страницы конкурируют за один URL
		_, _ = io.WriteString(w, "IMG")
	})
	for _, p := range []string{"/p1", "/p2", "/p3", "/p4"} {
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `<img src="/shared.png">`)
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<a href="/p1">1</a><a href="/p2">2</a><a href="/p3">3</a><a href="/p4">4</a>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := crawler.Analyze(context.Background(), crawler.Options{
		URL:         srv.URL,
		Depth:       2,
		Concurrency: 4,
		HTTPClient:  srv.Client(),
	})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Errorf("shared asset requested %d times under concurrency, want 1 (single-flight broken)", got)
	}
}
