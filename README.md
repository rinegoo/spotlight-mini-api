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
| `RESTRICTIONS_REGION` / `-restrictions-region` | `RU` | юрисдикция заведения для ограничений контента (`none` — выключить) |
| `RESTRICTIONS_POLICY_<ВИД>` | по закону | ужесточить политику вида: `RESTRICTIONS_POLICY_RU_INOAGENT=hide`; ниже минимума — нельзя |
| `RESTRICTIONS_SYNC_INTERVAL` / `-restrictions-sync` | `24h` | сервер сам обновляет реестры (`0` — только внешние утилиты) |
| `RESTRICTIONS_LABEL_SCALE` / `-restrictions-label-scale` | `2` | размер указания относительно основного текста (по закону — 2) |

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

## Ограничения контента (`internal/restrictions`)

Модуль ограничений: официальные реестры + ручной список → указание у песни или скрытие.
Уровни: `restrictions` (ядро, регионы и политики, ручной список) → `restrictions/ru` (виды РФ:
формы указаний и обязательные минимумы) → источники `restrictions/ru/inoagents` (реестр
иностранных агентов Минюста) и `restrictions/ru/extremist` (федеральный список экстремистских
материалов, только музыкальные записи).

**Регион и политики.** `RESTRICTIONS_REGION=RU` включает виды юрисдикции РФ с минимумами по закону:

| Вид | По умолчанию | Минимум | |
|---|---|---|---|
| `ru.inoagent` | `label` | `label` | указание по форме постановления № 2108; можно `hide` |
| `ru.extremist` | `hide` | `hide` | распространение запрещено (ст. 20.29 КоАП) |
| `ru.court_ban` | `hide` | `hide` | запрет решением суда (ручные правила по песням) |

Ослабить политику ниже минимума нельзя: берётся минимум, в журнале — предупреждение, в
`/api/stats` — `requested` и действующая политика. Несколько ограничений у песни — действует
самое строгое. Виды других юрисдикций не применяются.

**Данные** — в `DATA_DIR/restrictions/`: файлы источников `<id>.json` и `manual.yaml` (ручной
список, пример — [`deploy/restrictions-manual.example.yaml`](deploy/restrictions-manual.example.yaml)).
Источники независимы: сервер применяет **все** `*.json` в каталоге, кто бы их ни записал
(своя синхронизация раз в `RESTRICTIONS_SYNC_INTERVAL`, утилиты `restrictions-*`, cron), и раз в
30 с проверяет файлы — внешняя синхронизация и правки `manual.yaml` применяются без перезапуска.
Запись — через уникальный временный файл и переименование: одновременные синхронизации не
портят данные.

- Автоматически применяются совпадения по **псевдонимам** физлиц из реестра иноагентов и — для
  экстремистских материалов — «название песни в кавычках + исполнитель целым словом в описании»
  (пропуск опаснее лишнего скрытия; ложные — в `ignore`). Совпадения по ФИО (однофамильцы) и
  названиям организаций — только кандидаты: их подтверждают (`add`) или отклоняют (`ignore`) в
  `manual.yaml`. Там же — сценические имена и группы (к записи участника).
- Пометка снимается сама, когда у записи реестра появляется дата исключения (и для записей из
  `manual.yaml`, которые на неё ссылаются).
- Указание для иноагентов — по форме постановления Правительства № 2108 (ред. № 1232);
  переопределяется в `manual.yaml` (`labels.ru.inoagent`). В ответе поиска — `restrictions[]`
  (`kind`, `subject`, `label`, `note`); в `/api/stats` — `restrictions` (политика, сколько
  помечено/скрыто/на проверке, `labelScale`).
```sh
restrictions-ru-inoagents -data ./data -sync                     # реестр иноагентов Минюста
restrictions-ru-extremist -data ./data -sync                     # список экстремистских материалов
restrictions-ru-extremist -data ./data -file exportfsm.csv       # из локальной выгрузки
restrictions-ru-inoagents -data ./data -catalog base.db          # отчёт по всем источникам:
                                                                 # что применяется, что проверить
```

Правовой разбор и решения — `docs/content-restrictions/` в корне проекта Spotlight.

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
