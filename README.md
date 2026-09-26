# spotlight-mini-api

API поиска по каталогу караоке. Читает выгрузку каталога из Encore (`.xls`),
строит полнотекстовый индекс (Bleve) и отдаёт поиск с исправлением опечаток.
Веб-интерфейс — [spotlight-mini-web](https://github.com/rinegoo/spotlight-mini-web).

## Запуск

```sh
CATALOG_FILE=/path/to/catalog.xls go run ./cmd/catalog-server
```

| Переменная / флаг | По умолчанию | |
|---|---|---|
| `CATALOG_FILE` / `-catalog` | — (обязательно) | файл выгрузки каталога |
| `CATALOG_ADAPTER` / `-adapter` | автодетект | принудительный выбор адаптера (`encore_xls`) |
| `CATALOG_WATCH_INTERVAL` / `-watch` | `30s` | проверка файла на изменения (`0` — выкл.) |
| `LISTEN_ADDR` / `-addr` | `:8080` | |

Файл каталога загружается на сервер вручную. Если его ещё нет или он битый,
сервер всё равно стартует (поиск отвечает `503`) и подхватит файл, как только тот
появится. При замене файла новой выгрузкой индекс перестраивается (~2 с на 105 тыс.
песен) и подменяется без простоя; если новый файл не разобрался, остаётся
предыдущая версия.

## API

- `GET /api/search?q=&mode=all|artist|title&back=yes|no&artist=&limit=&offset=`
- `GET /api/stats` — число песен и исполнителей, время загрузки
- `GET /health` — всегда `200`, в теле `catalog: loaded | not_loaded`

Поиск: регистр, `ё/е`, диакритика и пунктуация не важны; опечатки (1 правка от 4
букв, 2 — от 8); префикс последнего слова; неверная раскладка (`fufnf` → `агата`);
частичные совпадения, если все слова не находятся.

## Адаптер `encore_xls`

Колонки `№ | Наименование | Исполнитель | бэк | тип`, строки сверх 65 535
продолжаются на следующих листах без заголовка. `бэк` = `•` — бэк-вокал, `тип` —
формат караоке-файла (EMP). Новые форматы — реализация `catalog.Adapter`.

## Сборка и деплой

GitHub Actions (`.github/workflows/ci.yml`): на каждый push и PR — `gofmt`,
`go vet`, `go test -race`; затем сборка Docker-образа и публикация в
`ghcr.io/rinegoo/spotlight-mini-api` (в PR — только сборка, без публикации).

| Событие | Теги образа |
|---|---|
| push в `main` | `latest`, `sha-<commit>` |
| тег `v1.2.3` | `1.2.3`, `1.2`, `sha-<commit>` |

Запуск на сервере — [`deploy/`](deploy): `docker-compose.yml` поднимает API и веб
из готовых образов.

```sh
cd deploy
cp .env.example .env     # CATALOG_DIR — папка с выгрузкой, CATALOG_FILENAME — имя файла
docker compose pull && docker compose up -d
```

Если пакеты в GHCR приватные, на сервере сначала нужно `docker login ghcr.io`
(токен с правом `read:packages`).
