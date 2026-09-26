# spotlight-mini-api

API поиска по каталогу караоке. Каталог — база песен плеера EnCore (`GET /BASE`,
присылает `encore-sync` из заведения) или выгрузка `.xls`; строит полнотекстовый
индекс (Bleve) и отдаёт поиск с исправлением опечаток.
Веб-интерфейс — [spotlight-mini-web](https://github.com/rinegoo/spotlight-mini-web).

## Запуск

```sh
CATALOG_FILE=/path/to/catalog.xls IMPORT_TOKEN=secret DATA_DIR=./data go run ./cmd/catalog-server
```

| Переменная / флаг | По умолчанию | |
|---|---|---|
| `IMPORT_TOKEN` / `-import-token` | пусто (импорт выключен) | Bearer-токен для `POST /api/import` |
| `DATA_DIR` / `-data` | пусто (не сохранять) | где хранить последний импорт — поднимается при старте |
| `CATALOG_FILE` / `-catalog` | пусто | файл каталога: XLS или `base.db` EnCore (запасной путь) |
| `CATALOG_ADAPTER` / `-adapter` | автодетект | принудительный выбор адаптера (`encore_base`, `encore_xls`) |
| `CATALOG_WATCH_INTERVAL` / `-watch` | `30s` | проверка файла на изменения (`0` — выкл.) |
| `LISTEN_ADDR` / `-addr` | `:8080` | |

Нужен хотя бы один источник: `IMPORT_TOKEN` и/или `CATALOG_FILE`. При старте
загружается сохранённый импорт из `DATA_DIR`, если его нет — файл. Пока загружен
импорт, изменения файла игнорируются.

Файл каталога загружается на сервер вручную. Если его ещё нет или он битый,
сервер всё равно стартует (поиск отвечает `503`) и подхватит файл, как только тот
появится. При замене файла новой выгрузкой индекс перестраивается (~2 с на 105 тыс.
песен) и подменяется без простоя; если новый файл не разобрался, остаётся
предыдущая версия.

## API

- `GET /api/search?q=&mode=all|artist|title|lyrics&back=yes|no&fav=1&artist=&limit=&offset=`
  - `mode=lyrics` — по словам текста песни (есть в базе EnCore у ~половины песен);
    в `all` текст тоже учитывается, но слабо и только точным словом;
  - `fav=1` — только избранное заведения (`optFav` в EnCore);
  - в ответе у песни: `number` (номер для оператора), `tabName` (только у песен не из
    основной вкладки), `vocalTrack` (есть дорожка с голосом — « +» в названии EnCore,
    из названия убирается), `favorite`, `backVocal`; `matches.lyrics` — найденные в тексте слова.
- `GET /api/stats` — песни, исполнители, избранное, песни со словами текста (`lyrics`),
  вкладки, источник (`kind`: `file` / `import`), хеш импорта, время загрузки
- `POST /api/import` — каталог от `encore-sync`: `Authorization: Bearer <IMPORT_TOKEN>`,
  тело — JSON `spotlight-catalog/1` (gzip). Ответ `imported` или `unchanged` (тот же хеш —
  индекс не перестраивается). Сайт (web) проксирует этот запрос — порт API открывать не нужно.
- `GET /health` — всегда `200`, в теле `catalog: loaded | not_loaded`

Поиск: регистр, `ё/е`, диакритика и пунктуация не важны; опечатки (1 правка от 4
букв, 2 — от 8); префикс последнего слова; неверная раскладка (`fufnf` → `агата`);
частичные совпадения, если все слова не находятся.

## Импорт из EnCore: `encore-sync`

Локальный бинарник для ПК в сети заведения. Снимает базу песен плеера
(`GET http://<encore>/BASE` — только чтение, так же делает пульт EncoreRC),
разбирает её и отправляет на сайт. Без зависимостей, собирается под Windows.

```sh
make -C .. encore-sync        # api/bin/encore-sync.exe (Windows) и api/bin/encore-sync

encore-sync -encore 192.168.75.20 -api https://karaoke.example.ru -token <IMPORT_TOKEN>
encore-sync -encore 192.168.75.20 -api … -token … -interval 6h   # по расписанию
encore-sync -base base.db -dry-run                                # только сводка
```

Переменные вместо флагов: `ENCORE_ADDR`, `SPOTLIGHT_API_URL`, `IMPORT_TOKEN`.
При `-interval` неизменившийся каталог повторно не отправляется. База весит ~150 МБ,
на сайт уходит ~12 МБ (gzip). Импорт полной базы на сервере — ~7 с.

## Адаптер `encore_base`

SQLite плеера EnCore (`GET /BASE`, подробно — `encore/docs/encore-base.md`):
вкладки `ntable` → таблицы `tabN`. Берутся `n` (номер), `song`, `singer`, `bek` (`•` —
бэк-вокал), `tip` (формат), `optFav` (`*` — избранное), `ftext` (слова текста).
Ключ песни — `вкладка:номер`. Читается драйвером `modernc.org/sqlite` (чистый Go).

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
cp .env.example .env     # IMPORT_TOKEN — для encore-sync; CATALOG_DIR/FILENAME — запасной файл
docker compose pull && docker compose up -d
```

Если пакеты в GHCR приватные, на сервере сначала нужно `docker login ghcr.io`
(токен с правом `read:packages`).

### Автодеплой через Portainer

После публикации образа из `main` CI (в обоих репозиториях) вызывает вебхук
стека Portainer Business Edition — Portainer скачивает свежие образы и
перезапускает стек.

1. Portainer → Stacks → стек каталога → Editor → **Webhooks** → включить
   *Create a stack webhook*, скопировать URL.
2. Если пакеты GHCR приватные: Portainer → Registries → добавить `ghcr.io`
   (пользователь GitHub + токен с `read:packages`).
3. В обоих репозиториях (`spotlight-mini-api`, `spotlight-mini-web`):
   Settings → Secrets and variables → Actions → секрет `PORTAINER_WEBHOOK_URL`.

URL вебхука должен быть доступен из интернета (его вызывают раннеры GitHub).
Без секрета шаг `deploy` пропускается.
