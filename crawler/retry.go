package crawler

import (
	"context"
	"net/http"
	"time"
)

// retryBaseDelay — базовая пауза перед повтором. Растёт экспоненциально с
// каждой попыткой, чтобы не генерировать бурст запросов к проблемному серверу.
const retryBaseDelay = 100 * time.Millisecond

// doRequest выполняет GET с повторами. Повтор делается только для временных
// проблем: сетевой сбой или статус 429/5xx. После opts.Retries дополнительных
// попыток возвращается результат последней из них. Отмена контекста немедленно
// прекращает дальнейшие попытки.
func doRequest(ctx context.Context, opts Options, lim *limiter, url string) (*http.Response, error) {
	dbg(url, "REQ %s", url)
	attempts := max(opts.Retries+1, 1)

	var resp *http.Response
	var err error

	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			if werr := waitRetry(ctx, attempt); werr != nil {
				return nil, werr
			}
		}
		if werr := lim.Wait(ctx); werr != nil {
			return nil, werr
		}

		var req *http.Request
		req, err = newRequest(ctx, opts, url)
		if err != nil {
			return nil, err
		}

		resp, err = opts.HTTPClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		if !retryableStatus(resp.StatusCode) {
			return resp, nil
		}
		if attempt < attempts-1 {
			_ = resp.Body.Close()
		}
	}

	return resp, err
}

// newRequest собирает GET-запрос с контекстом и заголовком User-Agent.
func newRequest(ctx context.Context, opts Options, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	setUserAgent(req, opts.UserAgent)
	return req, nil
}

// retryableStatus сообщает, стоит ли повторять запрос при этом коде: 429 или 5xx.
func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= http.StatusInternalServerError
}

// waitRetry выдерживает экспоненциально растущую паузу перед попыткой attempt,
// прерываясь при отмене контекста.
func waitRetry(ctx context.Context, attempt int) error {
	delay := retryBaseDelay << (attempt - 1)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
