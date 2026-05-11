# bybit-service

Веб-сервис, показывающий состояние **одного** Bybit-аккаунта **нескольким** пользователям, входящим под одним общим паролем. Бэкенд держит постоянное соединение с Bybit private WebSocket, хранит актуальный стейт (wallet + positions) в памяти и стримит обновления подключённым клиентам.

Фронт лежит в `web/` (Svelte 5 + Vite + Tailwind v4 + shadcn-style тёмная zinc-палитра), в проде собирается в статичный `dist/` и хостится nginx-ом, который заодно проксирует `/api/*` в Go-сервис.

## Архитектура

```
Bybit private WS ──┐
                   ├──► hub (in-memory state) ──► browser clients (WS)
Bybit REST (init) ─┘                          └─► /api/snapshot (HTTP)
        ▲
        └── pollMarginMode каждые 30с (account-level marginMode)
```

- Один процесс держит одно WS-соединение с Bybit и реплицирует его всем браузерным клиентам.
- Стейт никогда не персистится — это «зеркало» текущего состояния аккаунта.
- На старте делается REST-снапшот (`account/info` для marginMode + `wallet-balance` + `position/list` для linear USDT/USDC), затем подписка на WS-топики `wallet` и `position` приносит дельты.
- Параллельно горутина `pollMarginMode` каждые 30с дёргает `/v5/account/info` — Bybit не шлёт изменение marginMode (Cross↔Isolated на уровне аккаунта) через WS, и без поллинга UI не подхватывал бы переключение в Bybit-app.

В проде сервис стоит за **Cloudflare Origin Cert** (nginx → 127.0.0.1:8080), origin закрыт firewall'ом для всего трафика кроме CF edge-диапазонов. Подробности — в `deploy/` и `DEPLOY.local.md` (gitignored, у владельца инстанса).

## Карта кода

```
cmd/genpass/main.go         — утилита: пароль → bcrypt-хеш + session_secret (hex)
cmd/server/main.go          — точка входа: load config → hub → bybit.Client → http.Server
internal/config/config.go   — JSON-конфиг и его валидация
internal/auth/auth.go       — bcrypt-проверка + HMAC-подписанный stateless cookie
internal/bybit/sign.go      — HMAC-SHA256 (для REST-подписи и WS-auth)
internal/bybit/rest.go      — RESTClient: AccountInfo, WalletBalance, Positions (с пагинацией)
internal/bybit/ws.go        — WSClient: dial, auth, subscribe, ping/20s, reconnect 1→30s
internal/bybit/client.go    — оркестратор: REST snapshot → pollMarginMode goroutine → WS Run
internal/server/server.go   — http-роуты, login/logout/snapshot
internal/server/ws.go       — апгрейд /api/ws, ping/30s, pong-deadline 70s
internal/hub/hub.go         — состояние (Wallet, Positions map, Status); потокобезопасно;
                              mergePosition сохраняет старые поля при WS-дельтах с пустыми значениями;
                              ApplyMarginMode/ApplyWallet защищают marginMode от затирания WS-снапшотом

web/                        — Svelte 5 + Vite + Tailwind v4 (shadcn-стиль)
web/src/App.svelte          — root: tryBoot → snapshot/login routing; app.booting гасит мигание формы при F5
web/src/lib/api.ts          — login / logout / snapshot HTTP-клиент
web/src/lib/store.svelte.ts — глобальный $state ($state-runes file), connectWS с auto-reconnect
web/src/lib/types.ts        — типы Wallet/Position/Status/WSFrame, зеркало internal/hub/hub.go
web/src/lib/mockSeed.ts     — dev-only: фейковые wallet+positions для UI-итераций
web/src/lib/components/     — LoginScreen, Dashboard (activeTab → localStorage), WalletSummary, AssetsTab, PositionsTab

deploy/                     — артефакты для прод-инсталляции
deploy/bybit-mirror.service — systemd unit, User=bybit, ProtectSystem=strict, ReadOnlyPaths=/etc/bybit-mirror
deploy/bybit-mirror.nginx   — server-block: TLS на 443 (Cloudflare Origin Cert),
                              redirect 80→443, X-Robots-Tag/Referrer-Policy/X-Content-Type-Options,
                              robots.txt через `location = /robots.txt`,
                              map $http_upgrade $connection_upgrade в самом файле (попадает в http через sites-enabled)
deploy/nginx-connection-upgrade.conf — отдельный map-файл для conf.d-style nginx-сборок (не используется в текущем деплое)
```

Модуль Go называется `github.com/p-repin/bybit_mirror` (см. `go.mod`). Все внутренние импорты — через этот префикс.

## Поток данных

1. `cmd/server/main.go` стартует:
   - грузит конфиг,
   - создаёт `hub.Hub`,
   - инициализирует `auth.Manager`,
   - запускает `bybit.Client.Run(ctx)` в горутине,
   - запускает `http.Server`.
2. `bybit.Client.Run`:
   - сначала `snapshot()` — REST-вызовы `/v5/account/info` (marginMode), `/v5/account/wallet-balance`, `/v5/position/list` (linear, settleCoin=USDT и USDC). Результат заливается в hub через `ApplyMarginMode` / `ApplyWallet` / `ApplyPositions`.
   - параллельно стартует `pollMarginMode` — горутина с тикером 30с, дёргает `/v5/account/info` и вызывает `ApplyMarginMode` если значение изменилось (no-op + no broadcast если то же).
   - потом `WSClient.Run` — бесконечный цикл с реконнектом и backoff'ом.
3. WS-сообщения от Bybit разбираются в `WSClient.dispatch`:
   - `topic=wallet` → `hub.ApplyWallet` (перезапись, но `marginMode` сохраняется из старого Wallet если в новом пусто — WS-wallet топик не несёт marginMode) → broadcast `{type:"wallet"}`,
   - `topic=position` → `hub.ApplyPositions`. Для каждой позиции из дельты:
     - `size == "0"` → удаляем из map по ключу `category|symbol|positionIdx`;
     - позиция уже в map → `mergePosition`: пустые поля в дельте сохраняют старые значения (Bybit шлёт неизменившиеся поля как `""`, иначе их затирали бы);
     - новой позиции с пустым size — игнорируем (partial update без known позиции).
     Broadcast `{type:"positions"}`.
   - сервисные ответы `op=auth/subscribe/pong` логируются если `success=false`.
4. Браузер коннектится на `/api/ws`:
   - `hub.Register` сразу шлёт `{type:"snapshot", data:{wallet, positions, status}}`,
   - дальше получает дельты тех же типов плюс `{type:"status"}` при изменении состояния Bybit-канала.

## API контракт

| Метод   | Путь            | Ответ                                                       | Auth |
|---------|-----------------|-------------------------------------------------------------|------|
| `POST`  | `/api/login`    | `{"password":"..."}` → 204 + cookie `bybit_session`         | —    |
| `POST`  | `/api/logout`   | 204, чистит cookie                                          | —    |
| `GET`   | `/api/snapshot` | `{wallet, positions[], status}` JSON                        | ✅   |
| `GET`   | `/api/ws`       | WebSocket: snapshot → дельты `wallet` / `positions` / `status` | ✅   |

WebSocket-фрейм всегда:
```json
{ "type": "snapshot|wallet|positions|status", "data": ... }
```

Структуры `Wallet`, `Position`, `Status` — см. `internal/hub/hub.go`. JSON-теги повторяют поля v5 Bybit API один-в-один, поэтому десериализация WS-данных идёт прямо в эти типы.

**Wallet** имеет ещё поле `MarginMode string \`json:"marginMode,omitempty"\`` — оно не из Bybit-wallet-payload'а, а из `/v5/account/info`. UI рисует Cross/Isolated/Portfolio по этому полю (account-level), а не по `Position.TradeMode` (per-position). В UTA cross-аккаунте `TradeMode` почти всегда `0` независимо от того, что показывает Bybit-app, поэтому за визуальный режим отвечает `marginMode`.

**Position.TradeMode** — `*int` (указатель), чтобы `mergePosition` мог отличить «не пришло в дельте» (nil) от «реально 0». Сейчас в UI не используется, но поле сохраняется в payload для будущего расширения (если кто-то реально откроет per-position-isolated).

## Конфиг

Файл `config.json` (см. `config.example.json` за шаблоном):

| Поле                  | Назначение                                                          |
|-----------------------|---------------------------------------------------------------------|
| `listen_addr`         | где слушает HTTP, по умолчанию `:8080`                              |
| `environment`         | `mainnet` / `testnet` / `demo` (дефолт `testnet`) — задаёт префикс хоста (`api`/`api-testnet`/`api-demo`) |
| `region`              | пусто (=`bybit.com`) или `eu`, `nl`, `tr`, `kz`, `ae`, `id` — суффикс хоста (`bybit.<region>`). Для EU/MiCA-аккаунтов обязательно `eu` |
| `account_type`        | `unified` (дефолт) или `classic` — маппится в `UNIFIED`/`CONTRACT` для REST |
| `api_key`/`api_secret`| Bybit API key (требуются права на чтение wallet/position)           |
| `password_hash`       | bcrypt-хеш общего пароля (генерится `cmd/genpass`)                  |
| `session_secret`      | ключ HMAC для подписи cookie (минимум 16 байт; genpass даёт 32 hex) |
| `session_ttl_hours`   | TTL cookie, дефолт 24                                               |
| `cookie_insecure`     | `true` для локального HTTP-теста (без TLS); по умолчанию `false`    |

## Аутентификация

- Один общий пароль, верифицируется через `bcrypt.CompareHashAndPassword` (cost=12 ≈ 250ms — это де-факто rate limit).
- Сессия — **stateless**, без серверного стора. Формат cookie:
  ```
  <16 random bytes hex>.<unix_expires>.<HMAC-SHA256 от первых двух частей>
  ```
- Cookie: `HttpOnly`, `SameSite=Lax`, `Secure` (если не выставлен `cookie_insecure`).
- Все юзеры анонимны — отличить их в логах можно только по `remote_addr` и `session_id`.

## Деплой

Готовые артефакты лежат в `deploy/`. Базовая схема — **за Cloudflare-прокси с Origin Certificate**:

```
Browser ──[Cloudflare Universal SSL]──► Cloudflare Edge ──[Origin Cert TLS]──► VPS:443 (nginx)
                                                                                    │
                                                                          /api/* → 127.0.0.1:8080
                                                                          /     → /var/www/bybit-front
```

- nginx на 443 предъявляет **Cloudflare Origin Cert** (15-летний серт, валидный только для Cloudflare; публичные браузеры его не видят — они подключаются к Cloudflare Universal SSL).
- 80 → 301 на 443.
- `map $http_upgrade $connection_upgrade` лежит в `deploy/bybit-mirror.nginx` на верхнем уровне файла — попадает в `http`-контекст через `include sites-enabled/*`.
- Анти-индексация многослойно: `<meta name="robots">` в `index.html`, `X-Robots-Tag` HTTP-хедер, `/robots.txt` через `location =`-блок прямо в nginx.
- Origin-firewall: 80/443 пускают **только** Cloudflare-диапазоны (ufw + cron-скрипт `update-cf-ufw.sh` обновляет список раз в неделю).
- systemd unit с hardening (`NoNewPrivileges`, `ProtectSystem=strict`, `ReadOnlyPaths=/etc/bybit-mirror`, юзер `bybit`).
- `upgrader.CheckOrigin` возвращает `true` — фильтрация origin делается на nginx (за Cloudflare его как такового нет).

Конкретные команды деплоя/отката для текущего инстанса — в `DEPLOY.local.md` (gitignored, у владельца).

## Команды

```bash
# === Бэкенд ===

# одноразово: сгенерить хеш пароля и session_secret, вставить в config.json
go run ./cmd/genpass <password>

# собрать
go build ./...

# запустить
go run ./cmd/server -config config.json

# === Фронт (web/) ===

# одноразово: установить зависимости
cd web && npm install

# дев-сервер на :5173 с HMR; /api/* проксируется на :8080
npm run dev

# прод-сборка → web/dist (раздавать nginx-ом)
npm run build
```

Бэкенд и фронт обычно крутятся параллельно: Go-сервис на `:8080`, Vite-дев на `:5173`. С точки зрения браузера всё на одном origin благодаря Vite-proxy (см. `web/vite.config.ts`).

## Соглашения

- Логирование — `log/slog`, без отдельного логгера.
- Stdlib-роутинг (`http.ServeMux` с pattern syntax `"POST /api/..."`, требует Go 1.22+).
- `internal/hub/hub.go` — потокобезопасный (`sync.RWMutex`); все мутации через `Apply*` методы.
- Bybit DTO маппятся в типы `hub.Wallet`/`hub.Position` напрямую — не плодить параллельных структур.
- Без комментариев типа «что делает функция»; комментарий уместен только если объясняет неочевидное «почему».

## Текущий статус (2026-05-11)

Прод-инсталляция запущена и работает: домен `bb-mirror.com` через Cloudflare с Origin Certificate, mainnet API-ключи друга, end-to-end pipeline проверен (wallet, positions, marginMode подтягиваются и отображаются корректно).

**Что сделано в этой сессии (2026-05-11):**

*Бэкенд:*
- `internal/hub/hub.go` — `mergePosition`: WS-дельты Bybit могут приходить с пустыми полями для неизменившихся атрибутов (leverage, liqPrice и т.п.); раньше они затирали уже известные значения, теперь пустое поле сохраняет старое.
- `Position.TradeMode *int` — добавлено для возможного per-position cross/iso в будущем, но в UI не используется (см. ниже).
- `Wallet.MarginMode string` — account-level режим маржи, заполняется через `/v5/account/info`. WS-wallet-снапшоты этот поле не присылают, поэтому `ApplyWallet` сохраняет его из старого значения, а `ApplyMarginMode` пушит изменение отдельно.
- `internal/bybit/rest.go` — новый метод `AccountInfo()` дёргает `/v5/account/info`, возвращает `marginMode`.
- `internal/bybit/client.go` — в `snapshot()` сначала тянет `AccountInfo`, потом всё остальное. Запущена горутина `pollMarginMode` с тикером 30с (Bybit не присылает изменения marginMode через WS).

*Фронт:*
- `web/src/lib/components/PositionsTab.svelte` — бейдж `cross`/`iso`/`portfolio` рядом с LONG/SHORT × leverage; источник — `app.wallet?.marginMode`, не `p.tradeMode`. Если leverage пустой/`"0"` — `×N` не рисуется (UTA cross иногда так).
- `web/src/lib/store.svelte.ts` + `App.svelte` — `app.booting=true` initial, `finally { app.booting = false }` в tryBoot. Пока booting=true рендерится пустой `<div class="min-h-screen">`. Без этого при F5 на доли секунды мигал LoginScreen перед dashboard.
- `web/src/lib/components/Dashboard.svelte` — активная вкладка персистится в `localStorage` (`bybit-mirror.active-tab`), при F5 не сбрасывается на «Активы».
- `web/index.html` — `<meta name="robots" content="noindex, nofollow, noarchive, nosnippet">`.

*Инфра:*
- `deploy/bybit-mirror.service` — systemd unit, юзер `bybit`, hardening.
- `deploy/bybit-mirror.nginx` — server-block с TLS на 443 (Cloudflare Origin Cert) + 80→443 redirect + анти-индексация + robots.txt + WS-upgrade map.
- Прод-сервер: Ubuntu/Debian, /opt/bybit-mirror/bybit-mirror, /etc/bybit-mirror/config.json (mode 600 bybit:bybit), /var/www/bybit-front, ufw закрывает 80/443 для всех кроме Cloudflare-диапазонов, cron-скрипт `/usr/local/sbin/update-cf-ufw.sh` обновляет список раз в неделю.

**Открытые вопросы и не сделано:**
1. Адаптив таблицы монет в `AssetsTab` (сейчас `overflow-auto` — на телефоне горизонтальный скролл). По образцу `PositionsTab`: `md:hidden` карточки + `hidden md:block` таблица.
2. Cell-flash на апдейте (мигание ячейки при изменении PnL/цены — UX-приятность, как у бирж).
3. Retry-цикл для REST-снапшота на старте — сейчас одна попытка с 20с timeout, при флапе snapshot молча роняется и пользователь видит пустой UI до первого WS-события.
4. UI настроек для смены API-ключей через дашборд — обсуждалось, не делалось (вариант 3 в DEPLOY.local.md). Сейчас смена ключей — это `ssh + nano /etc/bybit-mirror/config.json + systemctl restart`.
5. Все сделанные изменения этой сессии — **ещё не закоммичены** в git. Будут отдельной серией коммитов по темам (deploy, hub fixes, marginMode, UI UX).

## Возможные расширения (если попросят)

- Добавить settleCoin'ы в конфиг (сейчас захардкожены `USDT`+`USDC` для linear-снапшота).
- Фильтр категорий позиций на бэке (сейчас WS-топик `position` без фильтра — теоретически придут все, что подписывается под аккаунтом).
- Rate limit на `/api/login` сверх bcrypt-задержки.
- Username при логине, если потребуется отображать в UI / логах.
- Адаптив таблицы монет в AssetsTab под мобилку (см. «Что дальше»).
- Cell-flash на апдейте (см. «Что дальше»).
- Сортировка / фильтры в таблицах (по PnL, по размеру).
- Тостеры/уведомления на ликвидацию или резкое движение PnL.
