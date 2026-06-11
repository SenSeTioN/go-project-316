package crawler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
)

// fetchResult — результат запроса к одному URL: статус, размер тела и ошибка
// (сетевая либо ошибка чтения тела). Используется и проверкой ссылок, и сбором
// ассетов, чтобы один URL запрашивался только один раз за весь обход.
type fetchResult struct {
	statusCode int
	sizeBytes  int64
	err        error
}

// resourceCache хранит результаты запросов по полному URL. Потокобезопасен,
// поэтому общий кэш переживает любое число горутин обхода. calls отслеживает
// «летящие» запросы, чтобы конкурентные обращения к одному URL дедуплицировались.
type resourceCache struct {
	mu    sync.Mutex
	items map[string]fetchResult
	calls map[string]*resourceCall
}

// resourceCall — единственный выполняемый запрос к URL. Остальные горутины ждут
// его завершения через wg и переиспользуют res (single-flight).
type resourceCall struct {
	wg  sync.WaitGroup
	res fetchResult
}

func newResourceCache() *resourceCache {
	return &resourceCache{
		items: make(map[string]fetchResult),
		calls: make(map[string]*resourceCall),
	}
}

// getResource возвращает результат запроса к url, обращаясь к транспорту не более
// одного раза за весь обход — даже при конкурентных вызовах: параллельные запросы
// того же URL ждут общий результат (single-flight). Ошибки отмены контекста не
// кэшируются — они временные и могут быть повторены позже.
func getResource(ctx context.Context, opts Options, lim *limiter, cache *resourceCache, url string) fetchResult {
	cache.mu.Lock()
	if r, ok := cache.items[url]; ok {
		cache.mu.Unlock()
		return r
	}
	if call, ok := cache.calls[url]; ok {
		cache.mu.Unlock()
		call.wg.Wait()
		return call.res
	}
	call := &resourceCall{}
	call.wg.Add(1)
	cache.calls[url] = call
	cache.mu.Unlock()

	call.res = loadResource(ctx, opts, lim, url)

	cache.mu.Lock()
	if ctx.Err() == nil {
		cache.items[url] = call.res
	}
	delete(cache.calls, url)
	cache.mu.Unlock()
	call.wg.Done()

	return call.res
}

// loadResource выполняет запрос и измеряет размер ответа. Если запрос не удался,
// возвращает fetchResult с одной лишь ошибкой.
func loadResource(ctx context.Context, opts Options, lim *limiter, url string) fetchResult {
	resp, err := doRequest(ctx, opts, lim, url)
	if err != nil {
		return fetchResult{err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	size, serr := measureSize(resp)
	return fetchResult{statusCode: resp.StatusCode, sizeBytes: size, err: serr}
}

// measureSize берёт размер из заголовка Content-Length, а если его нет —
// считает фактические байты тела. При ошибке чтения возвращает 0 и причину.
func measureSize(resp *http.Response) (int64, error) {
	if resp.ContentLength >= 0 {
		return resp.ContentLength, nil
	}
	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// isCancelErr сообщает, вызвана ли ошибка отменой или таймаутом контекста.
func isCancelErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
