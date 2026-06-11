// Command hexlet-go-crawler — CLI-обёртка над пакетом crawler: разбирает флаги, запускает обход сайта и печатает JSON-отчёт в stdout.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"code/crawler"

	"github.com/urfave/cli/v3"
)

func main() {
	cmd := &cli.Command{
		Name:      "hexlet-go-crawler",
		Usage:     "analyze a website structure",
		ArgsUsage: "<url>",
		Flags: []cli.Flag{
			&cli.IntFlag{Name: "depth", Value: 10, Usage: "crawl depth"},
			&cli.IntFlag{Name: "retries", Value: 1, Usage: "number of retries for failed requests"},
			&cli.DurationFlag{Name: "delay", Usage: "delay between requests (example: 200ms, 1s)"},
			&cli.DurationFlag{Name: "timeout", Value: 15 * time.Second, Usage: "per-request timeout"},
			&cli.IntFlag{Name: "rps", Usage: "limit requests per second (overrides delay)"},
			&cli.StringFlag{Name: "user-agent", Usage: "custom user agent"},
			&cli.IntFlag{Name: "workers", Value: 4, Usage: "number of concurrent workers"},
		},
		Action: run,
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

// run собирает Options из флагов и печатает отчёт. Пустой URL и сетевые ошибки не дают ненулевой код выхода: выводится справка либо сообщение в stderr.
func run(ctx context.Context, cmd *cli.Command) error {
	url := cmd.Args().First()
	if url == "" {
		fmt.Fprintln(os.Stderr, "error: URL is required")
		return cli.ShowAppHelp(cmd)
	}

	opts := crawler.Options{
		URL:         url,
		Depth:       cmd.Int("depth"),
		Retries:     cmd.Int("retries"),
		Delay:       cmd.Duration("delay"),
		RPS:         cmd.Int("rps"),
		Timeout:     cmd.Duration("timeout"),
		UserAgent:   cmd.String("user-agent"),
		Concurrency: cmd.Int("workers"),
		IndentJSON:  true,
		HTTPClient:  &http.Client{Timeout: cmd.Duration("timeout")},
	}

	data, err := crawler.Analyze(ctx, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return nil
	}

	fmt.Println(string(data))
	return nil
}
