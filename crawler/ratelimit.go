package crawler

import (
	"context"
	"sync"
	"time"
)

// limiter равномерно распределяет запросы по всему процессу: гарантирует, что
// между соседними вызовами Wait проходит не меньше interval. Потокобезопасен,
// поэтому один limiter обслуживает все горутины обхода. interval <= 0 означает
// отсутствие ограничения.
type limiter struct {
	interval time.Duration
	mu       sync.Mutex
	next     time.Time
}

func newLimiter(interval time.Duration) *limiter {
	return &limiter{interval: interval}
}

// rateInterval вычисляет интервал между запросами из настроек. RPS имеет
// приоритет над Delay; если ничего не задано — ограничения нет.
func rateInterval(opts Options) time.Duration {
	if opts.RPS > 0 {
		return time.Second / time.Duration(opts.RPS)
	}
	if opts.Delay > 0 {
		return opts.Delay
	}
	return 0
}

// Wait блокирует до следующего разрешённого момента. Возвращает ошибку
// контекста, если тот отменён — ожидание прерывается сразу, без зависания.
func (l *limiter) Wait(ctx context.Context) error {
	if l.interval <= 0 {
		return ctx.Err()
	}

	l.mu.Lock()
	now := time.Now()
	if l.next.Before(now) {
		l.next = now
	}
	wait := l.next.Sub(now)
	l.next = l.next.Add(l.interval)
	l.mu.Unlock()

	if wait <= 0 {
		return ctx.Err()
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
