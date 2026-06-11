### Hexlet tests and linter status:
[![Actions Status](https://github.com/SenSeTioN/go-project-316/actions/workflows/hexlet-check.yml/badge.svg)](https://github.com/SenSeTioN/go-project-316/actions)
[![CI](https://github.com/SenSeTioN/go-project-316/actions/workflows/ci.yml/badge.svg)](https://github.com/SenSeTioN/go-project-316/actions/workflows/ci.yml)

# hexlet-go-crawler

Утилита для анализа структуры сайта. Обходит страницы в пределах домена,
проверяет SEO-теги и битые ссылки, измеряет размер ассетов и печатает единый
JSON-отчёт.

## Использование

```bash
make install                            # установить зависимости
make build                              # собрать бинарь в bin/hexlet-go-crawler
make test                               # прогнать тесты
make lint                               # запустить линтер
make run URL=https://example.com        # запустить анализ
go run ./cmd/hexlet-go-crawler --help   # справка по флагам
```

Если `URL` не указан, утилита печатает справку. Код выхода всегда `0`, даже при
сетевых ошибках — детали попадают в поле `error` отчёта.

## Флаги

| Флаг | По умолчанию | Назначение |
| --- | --- | --- |
| `--depth` | `10` | глубина обхода (число переходов от стартового URL) |
| `--retries` | `1` | число дополнительных попыток при временной ошибке |
| `--delay` | — | фиксированная пауза между запросами (`200ms`, `1s`) |
| `--rps` | — | целевое число запросов в секунду (приоритетнее `--delay`) |
| `--timeout` | `15s` | таймаут HTTP-клиента |
| `--user-agent` | — | значение заголовка `User-Agent` |
| `--workers` | `4` | число воркеров обхода |

```bash
go run ./cmd/hexlet-go-crawler --depth 2 --rps 5 https://example.com
```

## Формат отчёта

В stdout печатается ровно один JSON-документ. Все ключи присутствуют всегда:
обходятся только ссылки `<a href>`; ресурсы (`img`/`script`/`link`) попадают в
`assets`. Поле `error` присутствует только когда непустое (`omitempty`).

```json
{
  "root_url": "https://example.com",
  "depth": 1,
  "generated_at": "2024-06-01T12:34:56Z",
  "pages": [
    {
      "url": "https://example.com",
      "depth": 0,
      "http_status": 200,
      "status": "ok",
      "seo": {
        "has_title": true,
        "title": "Example title",
        "has_description": true,
        "description": "Example description",
        "has_h1": true
      },
      "broken_links": [
        {
          "url": "https://example.com/missing",
          "status_code": 404,
          "error": "Not Found"
        }
      ],
      "assets": [
        {
          "url": "https://example.com/static/logo.png",
          "type": "image",
          "status_code": 200,
          "size_bytes": 12345
        }
      ],
      "discovered_at": "2024-06-01T12:34:56Z"
    }
  ]
}
```

| Поле | Описание |
| --- | --- |
| `root_url` | стартовый URL обхода |
| `depth` | заданный лимит глубины |
| `generated_at` | время формирования отчёта, ISO8601 (UTC) |
| `pages[].url` | URL страницы |
| `pages[].depth` | расстояние от стартового URL (`0` — стартовая) |
| `pages[].http_status` | HTTP-код ответа (`0` при сетевой ошибке) |
| `pages[].status` | `ok` или `error` |
| `pages[].error` | текст ошибки страницы (отсутствует при успехе) |
| `pages[].seo` | SEO-теги: `has_title`, `title`, `has_description`, `description`, `has_h1` |
| `pages[].broken_links[]` | недоступные `<a href>`-ссылки: `url`, `status_code`, `error` (`[]` при успехе, `null` у ошибочной страницы) |
| `pages[].assets[]` | ресурсы страницы (`url`, `type` `image`/`script`/`style`, `status_code`, `size_bytes`, `error` при ошибке); отсортированы по типу |
| `pages[].discovered_at` | время обработки страницы, ISO8601 (UTC) |

## Поведение обхода

- **Глубина.** Стартовая страница — `depth = 0`, её ссылки — `depth = 1` и так
  далее. Обходятся уровни от `0` до `depth - 1`: `--depth 1` обработает только
  стартовую страницу. По ссылкам переходим лишь внутри домена-источника; внешние
  страницы не обходятся, но их доступность проверяется в `broken_links`. Каждая
  страница попадает в отчёт не более одного раза.
- **Скорость.** `--delay` или `--rps` ограничивают частоту **всех** HTTP-запросов
  процесса. Без этих флагов скорость не ограничивается.
- **Повторы.** `--retries` задаёт число дополнительных попыток при временной
  проблеме (сетевой сбой, статус `429` или `5xx`); для прочих кодов повтор не
  делается. Между попытками выдерживается растущая пауза, в отчёт попадает
  результат последней попытки.
- **Ассеты.** Один и тот же URL (ассета или ссылки) запрашивается за весь обход
  только один раз — результаты кэшируются. `size_bytes` берётся из
  `Content-Length`, а при его отсутствии считается по телу ответа.
- **Отмена.** Прерывание обхода оставляет валидный JSON с уже собранными
  страницами.
