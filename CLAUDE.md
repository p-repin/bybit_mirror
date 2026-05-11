# bybit-service

Веб-сервис, показывающий состояние **одного** Bybit-аккаунта **нескольким** пользователям, входящим под одним общим паролем. Бэкенд держит постоянное соединение с Bybit private WebSocket, хранит актуальный стейт (wallet + positions) в памяти и стримит обновления подключённым клиентам.

Фронт лежит в `web/` (Svelte 5 + Vite + Tailwind v4 + shadcn-style тёмная zinc-палитра), в проде собирается в статичный `dist/` и хостится nginx-ом, который заодно проксирует `/api/*` в Go-сервис.

## Архитектура

```
Bybit private WS ──┐
                   │
Bybit public WS    ├──► hub (in-memory state) ──► browser clients (WS)
  /linear (mark)   │                          └─► /api/snapshot (HTTP)
  /option (mark)   │
                   │
Bybit REST (init) ─┘
        ▲
        └── pollMarginMode каждые 30с (account-level marginMode)
```

- Один процесс держит соединения с Bybit и реплицирует их всем браузерным клиентам.
- Стейт никогда не персистится — это «зеркало» текущего состояния аккаунта.
- На старте делается REST-снапшот (`account/info` для marginMode + `wallet-balance` + `position/list` для **linear** USDT/USDC и **option** USDT/USDC), затем подписка на WS-топики `wallet` и `position` приносит дельты.
- Параллельно горутина `pollMarginMode` каждые 30с дёргает `/v5/account/info` — Bybit не шлёт изменение marginMode (Cross↔Isolated на уровне аккаунта) через WS, и без поллинга UI не подхватывал бы переключение в Bybit-app.
- **Bybit private `wallet`/`position` event-driven** — пушат на филах, funding, leverage change, ликвидациях, **не** на каждое движение mark-цены. Поэтому отдельно держим **два public-WS соединения** к `/v5/public/linear` и `/v5/public/option` (Bybit разделил public-фиды по категориям), подписываемся на `tickers.<symbol>` для каждой открытой позиции. На тике зовём `hub.ApplyMarkPrice(category, symbol, markPrice)`, который пересчитывает `unrealisedPnl` + `positionValue` по позиции и через `recomputeWalletLocked` — wallet-агрегаты (per-coin UPL/equity/usdValue, totalPerpUPL, totalEquity). Без этого PnL в UI замирал между событиями.

В проде сервис стоит за **Cloudflare Origin Cert** (nginx → 127.0.0.1:8080), origin закрыт firewall'ом для всего трафика кроме CF edge-диапазонов. Подробности — в `deploy/` и `DEPLOY.local.md` (gitignored, у владельца инстанса).

## Карта кода

```
cmd/genpass/main.go         — утилита: пароль → bcrypt-хеш + session_secret (hex)
cmd/server/main.go          — точка входа: load config → hub → bybit.Client → http.Server
internal/config/config.go   — JSON-конфиг и его валидация
internal/auth/auth.go       — bcrypt-проверка + HMAC-подписанный stateless cookie
internal/bybit/sign.go      — HMAC-SHA256 (для REST-подписи и WS-auth)
internal/bybit/rest.go      — RESTClient: AccountInfo, WalletBalance, Positions (с пагинацией);
                              Positions тегает p.SettleCoin из контекста запроса
internal/bybit/ws.go        — WSClient (private): dial, auth, subscribe wallet+position,
                              ping/20s, reconnect 1→30s
internal/bybit/ws_public.go — WSPublicClient: public-стрим конкретной категории
                              (linear или option), подписывается на tickers.<symbol> по
                              текущему набору открытых позиций, sub/unsub батчами по 10,
                              реконнект 1→30с, ping/20с. На тике зовёт hub.ApplyMarkPrice.
internal/bybit/client.go    — оркестратор: REST snapshot → pollMarginMode → два
                              WSPublicClient (linear+option) + один WSClient (private).
                              Хук hub.SetOnPositionsChanged синкает подписки обоих
                              public-стримов под актуальный набор позиций
internal/server/server.go   — http-роуты, login/logout/snapshot
internal/server/ws.go       — апгрейд /api/ws, ping/30s, pong-deadline 70s
internal/hub/hub.go         — состояние (Wallet, Positions map, Status); потокобезопасно;
                              mergePosition сохраняет старые поля при WS-дельтах с пустыми значениями;
                              ApplyMarginMode/ApplyWallet защищают marginMode от затирания WS-снапшотом;
                              ApplyMarkPrice пересчитывает PnL+positionValue под mark-тики;
                              recomputeWalletLocked собирает wallet-агрегаты (coin.UPL/equity/usdValue,
                              totalPerpUPL, totalEquity) из текущих позиций;
                              sortedPositions держит детерминированный порядок в broadcast'ах
                              (без неё Go-рандомизация итерации по map'е перетряхивала строки в UI)

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
   - регистрирует хук `hub.SetOnPositionsChanged` — при изменении набора открытых символов синкает подписки обоих public-WS (`wsLin` на `hub.Symbols("linear")`, `wsOpt` на `hub.Symbols("option")`).
   - дальше `snapshot()` — REST-вызовы `/v5/account/info` (marginMode), `/v5/account/wallet-balance`, `/v5/position/list` для **4 пар**: `linear/USDT`, `linear/USDC`, `option/USDT`, `option/USDC` (опции без явного pull'а никогда не попадали в state — WS `position` event-driven). Результат заливается в hub через `ApplyMarginMode` / `ApplyWallet` / `ApplyPositions`. Каждая позиция тегается `SettleCoin` из контекста запроса в `rest.Positions`.
   - стартует `pollMarginMode` — горутина с тикером 30с, дёргает `/v5/account/info` и вызывает `ApplyMarginMode` если значение изменилось.
   - стартует две public-WS горутины: `wsLin.Run` (endpoint `/v5/public/linear`) и `wsOpt.Run` (endpoint `/v5/public/option`). Каждая держит свой реконнект-цикл с backoff'ом 1→30с и пингом каждые 20с.
   - потом `WSClient.Run` (private) — бесконечный цикл с реконнектом и backoff'ом.
3. WS-сообщения от Bybit:
   - **Private** `WSClient.dispatch`:
     - `topic=wallet` → `hub.ApplyWallet` (перезапись, но `marginMode` сохраняется из старого Wallet если в новом пусто — WS-wallet топик не несёт marginMode; ApplyWallet тут же зовёт `recomputeWalletLocked` чтобы агрегаты были консистентны с позициями) → broadcast `{type:"wallet"}`,
     - `topic=position` → `hub.ApplyPositions`. Для каждой позиции из дельты:
       - `size == "0"` → удаляем из map по ключу `category|symbol|positionIdx`;
       - позиция уже в map → `mergePosition`: пустые поля в дельте сохраняют старые значения (Bybit шлёт неизменившиеся поля как `""`, иначе их затирали бы); `SettleCoin` тоже сохраняется через merge;
       - новой позиции с пустым size — игнорируем (partial update без known позиции).
       Дёргается `recomputeWalletLocked`, бродкастится `{type:"positions"}` + `{type:"wallet"}`. Если изменился набор символов — `onPositionsChanged` синкает подписки public-WS.
     - сервисные ответы `op=auth/subscribe/pong` логируются если `success=false`.
   - **Public** (`linear` или `option`) `WSPublicClient.dispatch`:
     - `topic=tickers.<symbol>` → берём `markPrice` из payload'а → `hub.ApplyMarkPrice(c.category, symbol, mark)`.
4. `hub.ApplyMarkPrice(category, symbol, mark)`:
   - находит позиции с этим `Symbol`+`Category`, обновляет `markPrice`, пересчитывает `unrealisedPnl = (mark − avg) × size` (знак по `Side`) и `positionValue = size × mark` для linear-математики.
   - дёргает `recomputeWalletLocked`: для каждой позиции по `SettleCoin` (fallback — `settleCoinFromSymbol` по суффиксу символа) суммирует UPL в `coinPnl[settle]`; обновляет `wallet.coin[USDT|USDC].unrealisedPnl`, `equity = walletBalance + pnl`, `usdValue = equity` (для стейблов, 1:1); агрегирует `wallet.totalPerpUPL = ΣPnL` и `wallet.totalEquity = ΣusdValue`.
   - бродкастит `{type:"positions"}` + `{type:"wallet"}`.
5. Браузер коннектится на `/api/ws`:
   - `hub.Register` сразу шлёт `{type:"snapshot", data:{wallet, positions, status}}` (позиции через `sortedPositions` — стабильный порядок),
   - дальше получает дельты тех же типов плюс `{type:"status"}` при изменении состояния private-канала Bybit.

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

**Что сделано в этой сессии (2026-05-11, итерация 2) — real-time mark + опционы + мобилка:**

*Бэкенд (`internal/bybit/`, `internal/hub/`, `internal/config/`):*
- **`ws_public.go`** — новый public-WS клиент, параметризован категорией (`linear`/`option`). Подписывается на `tickers.<symbol>` для текущих открытых позиций, sub/unsub батчами по 10 args (под лимит Bybit), реконнект 1→30с, ping/20с. На тике зовёт `hub.ApplyMarkPrice(c.category, symbol, mark)`. Линейный и опционный держим раздельными экземплярами.
- **`config.go`** — добавлены `BybitPublicLinearWSURL()` и `BybitPublicOptionWSURL()`. Demo делит public-стрим с mainnet, отдельного demo-public у Bybit нет; testnet имеет свой.
- **`client.go`** — оркестратор теперь стартует **2 public-WS** (linear+option) + private + pollMarginMode. Хук `hub.SetOnPositionsChanged` синкает подписки обоих стримов через `hub.Symbols("linear")` / `hub.Symbols("option")`. REST-снапшот расширен с 2 до 4 пар: добавлены `option/USDT` и `option/USDC` (до этого опции попадали в state только случайно через WS-дельту и пропадали после рестарта сервиса).
- **`hub.go::ApplyMarkPrice`** — пересчитывает `unrealisedPnl` (формула `(mark − avg) × size`, знак по Side) и `positionValue` (`size × mark`) для позиций матчащих symbol+category. Дёргает `recomputeWalletLocked`, бродкастит `positions` + `wallet`.
- **`hub.go::recomputeWalletLocked`** — собирает wallet-агрегаты из текущих позиций: per-coin UPL/equity группируется по `SettleCoin`; для USDT/USDC `usdValue = equity` (1:1); `totalPerpUPL = ΣPnL`, `totalEquity = ΣusdValue`. Вызывается из `ApplyMarkPrice`, `ApplyPositions`, `ApplyWallet` — wallet всегда консистентен с позициями.
- **`hub.go::Position.SettleCoin`** — новое поле, тегается в `rest.Positions` из параметра запроса (Bybit непоследовательно отдаёт это поле в payload'е). `mergePosition` сохраняет его через WS-дельты. Используется в `recomputeWalletLocked` как primary источник; fallback — `settleCoinFromSymbol` (эвристика по суффиксу).
- **`hub.go::sortedPositions`** — стабильная сортировка по `symbol → side → positionIdx` во всех 4 местах где map разворачивалась в slice (ApplyPositions, ApplyMarkPrice, Snapshot, snapshotEnvelope). Без неё Go-рандомизация итерации по map'е перетряхивала строки в UI на каждом broadcast'е.
- **`hub.go::SetOnPositionsChanged(cb)`** — хук срабатывает после `ApplyPositions` если изменился **набор символов** (не сами поля). Используется оркестратором для синка public-WS подписок.

*Фронт (`web/src/lib/components/`):*
- **`AssetsTab.svelte`** — `md:hidden` карточки + `hidden md:block` таблица, по образцу `PositionsTab`. Раньше overflow-auto давал горизонтальный скролл на iPhone 12 mini.

*Косяки и почему (для меня в следующий раз):*
- **Изначально предполагал что Bybit `wallet` private-топик пушит per-tick `totalPerpUPL`** — это было неверно. Топик event-driven (фил, funding, leverage change). После первого деплоя real-time'а юзер заметил «нижний PnL движется, верхний — нет». Пришлось добавить серверный пересчёт wallet-агрегатов (`recomputeWalletLocked`).
- **`settleCoinFromSymbol` ломалось на опционах** — символ `ETHUSDT-12MAY26-2325-C` не подходит ни под `…USDT`, ни под `…USDC`. Опция не попадала в `coin.USDT.unrealisedPnl`, totalPerpUPL занижался. Пришлось добавить `Position.SettleCoin` с тегированием из REST-контекста.
- **Опционы пропали после рестарта сервиса** — REST-снапшот их не тянул вообще (только `linear`), а WS `position` event-driven. До рестарта опция оставалась в памяти от случайной WS-дельты, после — исчезла. Расширили pull на `category=option`.
- **Позиции перетряхивались в UI** — Go map iteration randomized; добавили `sortedPositions`.
- **Опции долго не тикали в реал-тайме**, хотя linear уже тикал — Bybit держит public/linear и public/option на **разных endpoint'ах**, нужны **два** WS-соединения. Параметризовал `WSPublicClient` категорией.

**Открытые вопросы:**
1. Подтвердить от юзера: `Total Equity` в нашей шапке должна совпадать с «105 USD» из Bybit-app (главный экран UTA). Wallet Balance 106.20 у нас и у Bybit-app должны совпадать (это разные числа от Total Equity!). Если Total Equity у нас не 105 — копать дальше.
2. `Available` (`totalAvailableBalance`) серверно не пересчитываем — обсуждали и отвергли (точная формула зависит от cross/iso/portfolio + IM-laddering, приближение разошлось бы с Bybit-app).
3. `Liq Price` не тикает — реплицировать Bybit-модель сложно, оставили event-driven.
4. Inverse-перпы и опции вне USDT/USDC settle не поддержаны — у друга их нет.
5. Cell-flash на апдейте — всё ещё в TODO. Mark/PnL/positionValue тикают, можно вешать.
6. Retry-цикл для REST-снапшота на старте — одна попытка с 20с timeout, при флапе snapshot тихо роняется.
7. UI настроек для смены API-ключей через дашборд — пока ssh+systemctl restart.
8. Все изменения этой сессии — **ещё не закоммичены** в git. Будут серией коммитов по темам (public WS, hub recompute, sort, option support, mobile AssetsTab).

## Возможные расширения (если попросят)

- Добавить settleCoin'ы в конфиг (сейчас захардкожены `USDT`+`USDC` для linear- и option-снапшота; для других settle нужно расширять `client.go::snapshot`).
- Inverse-перпы (`category=inverse`): отдельный REST-pull, формула PnL `(1/avg − 1/mark) × size` (не линейная), и третий public-WS на `/v5/public/inverse`.
- Rate limit на `/api/login` сверх bcrypt-задержки.
- Username при логине, если потребуется отображать в UI / логах.
- Cell-flash на апдейте (UX-приятность; ключевое — `markPrice`/`unrealisedPnl` уже тикают, есть на чём вешать).
- Юзерская сортировка / фильтры в таблицах (по PnL, по размеру). Серверный `sortedPositions` даёт стабильный baseline.
- Тостеры/уведомления на ликвидацию или резкое движение PnL.
- Approx `totalAvailableBalance` сервёрно — обсуждали и отвергли (расходилось бы с Bybit-app на пару %; точная формула зависит от cross/iso/portfolio + IM/MMR ladders). Если надо — `equity − Σ(positionValue/leverage)` как старт.
