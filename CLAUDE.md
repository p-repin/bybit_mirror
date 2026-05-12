# bybit-service

Веб-сервис, показывающий состояние **одного** Bybit-аккаунта **нескольким** пользователям, входящим под одним общим паролем. Бэкенд держит постоянное соединение с Bybit private WebSocket, хранит актуальный стейт (wallet + positions) в памяти и стримит обновления подключённым клиентам.

Фронт лежит в `web/` (Svelte 5 + Vite + Tailwind v4 + shadcn-style тёмная zinc-палитра), в проде собирается в статичный `dist/` и хостится nginx-ом, который заодно проксирует `/api/*` в Go-сервис.

## Архитектура

```
Bybit private WS ──┐                                                ┌─► browser clients (WS)
                   │                                                │
Bybit public WS    ├──► hub (in-memory state) ──────────────────────┤
  /linear (mark)   │                                                │
  /option (mark)   │                                                └─► /api/snapshot (HTTP)
                   │
Bybit REST ────────┘
  pollWallet 2с      — /v5/account/wallet-balance (canonical totals)
  pollPositions 10с  — /v5/position/list × 4 (safety-net)
  pollMarginMode 30с — /v5/account/info (account-level marginMode)
  snapshot           — то же что pollWallet+pollPositions+pollMarginMode на старте
```

- Один процесс держит соединения с Bybit и реплицирует их всем браузерным клиентам.
- Стейт никогда не персистится — это «зеркало» текущего состояния аккаунта.
- **Гибридная схема**: реал-тайм тики приходят из public-WS, авторитетные суммы — из REST-поллов.
  - **public-WS** (`/v5/public/linear` + `/v5/public/option`, разные endpoint'ы) подписан на `tickers.<symbol>` для каждой открытой позиции. На тике `hub.ApplyMarkPrice` обновляет `markPrice`, пересчитывает `unrealisedPnl = (mark−avg)×size` и `positionValue = size×mark`, пересобирает `totalPerpUPL = Σ position.unrealisedPnl` — это даёт sub-second движение цифр в UI.
  - **REST `pollWallet` 2с** — `/v5/account/wallet-balance` → `hub.ApplyWallet`. Bybit отдаёт `totalEquity`, `totalWalletBalance`, `totalAvailableBalance`, per-coin `equity/usdValue/walletBalance/unrealisedPnl` уже с учётом bonus, IM-локов, спот-цены не-стейбл монет — реплицировать всё это локально нерально и расходилось бы с Bybit-app. Шапка обновляется раз в 2с, но `totalPerpUPL` продолжает тикать sub-second.
  - **REST `pollPositions` 10с** — safety-net поверх WS. Перечитывает все 4 (linear/USDT, linear/USDC, option/USDT, option/USDC), зовёт `ApplyPositions` (re-tag category, baseline `liqPrice`/`avgPrice`), и принудительно пинает `SetSymbols` на обоих public-стримах — это триггерит `syncSubs` и резабит то, что Bybit мог отвергнуть.
  - **REST `pollMarginMode` 30с** — `/v5/account/info` (Bybit не пушит изменения marginMode через WS).
- **WS-private** (`wallet`/`position`) event-driven (фил, funding, leverage, ликвидация). Используется для немедленной реакции на сделки — не ждать следующий REST-полл.

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
                              syncSubs обновляет c.active ПЕССИМИСТИЧНО (только после
                              успешного send) — иначе rejected subscribe залипал бы до
                              реконнекта. dispatch снимает символы с c.active при
                              op=subscribe success=false, чтобы следующий sync повторил.
internal/bybit/client.go    — оркестратор: REST snapshot → pollWallet (2с) + pollPositions
                              (10с) + pollMarginMode (30с) + два WSPublicClient
                              (linear+option) + один WSClient (private).
                              pollWallet тянет авторитетные wallet-totals от Bybit
                              (не считаем сами); pollPositions = safety-net поверх WS,
                              принудительно пинает SetSymbols на обоих public-стримах
                              чтобы syncSubs пересубнул то, что было отвергнуто.
                              Хук hub.SetOnPositionsChanged синкает подписки при
                              изменении набора символов через WS-дельты (immediate path).
internal/server/server.go   — http-роуты, login/logout/snapshot
internal/server/ws.go       — апгрейд /api/ws, ping/30s, pong-deadline 70s
internal/hub/hub.go         — состояние (Wallet, Positions map, Status); потокобезопасно;
                              mergePosition сохраняет старые поля при WS-дельтах с пустыми значениями;
                              ApplyMarginMode/ApplyWallet защищают marginMode от затирания WS-снапшотом;
                              ApplyPositions: для новой позиции с пустым Category дефолтит
                              через categoryFromSymbol (дефис → option, иначе linear) —
                              иначе Symbols(category) фильтр отбрасывал её, public-WS не
                              подписывался, новая позиция «застывала» до рестарта;
                              ApplyMarkPrice пересчитывает PnL+positionValue под mark-тики;
                              recomputeWalletLocked теперь ТОЛЬКО считает totalPerpUPL =
                              Σ position.unrealisedPnl (остальные wallet-поля canonical
                              через REST pollWallet);
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
   - регистрирует хук `hub.SetOnPositionsChanged` — при изменении набора открытых символов синкает подписки обоих public-WS (`wsLin` на `hub.Symbols("linear")`, `wsOpt` на `hub.Symbols("option")`). Это immediate-path на WS-position event.
   - дальше `snapshot()` — REST-вызовы `/v5/account/info` (marginMode), `/v5/account/wallet-balance`, `/v5/position/list` для **4 пар**: `linear/USDT`, `linear/USDC`, `option/USDT`, `option/USDC` (опции без явного pull'а никогда не попадали в state — WS `position` event-driven). Результат заливается в hub через `ApplyMarginMode` / `ApplyWallet` / `ApplyPositions`. Каждая позиция тегается `SettleCoin` и `Category` из контекста запроса в `rest.Positions`.
   - стартует **4 горутины поллинга**:
     - `pollMarginMode` (30с) — `/v5/account/info` → `ApplyMarginMode` если изменилось.
     - `pollWallet` (2с) — `/v5/account/wallet-balance` → `ApplyWallet`. Это **главный источник** для `totalEquity`/`totalWalletBalance`/`totalAvailableBalance`/per-coin полей; Bybit считает их сам с учётом bonus/IM-локов/спот-цены.
     - `pollPositions` (10с) — все 4 категории → `ApplyPositions` → принудительный `SetSymbols` на обоих public-стримах (триггерит `syncSubs` для resub того, что Bybit отверг).
   - стартует две public-WS горутины: `wsLin.Run` (endpoint `/v5/public/linear`) и `wsOpt.Run` (endpoint `/v5/public/option`). Каждая держит свой реконнект-цикл с backoff'ом 1→30с и пингом каждые 20с.
   - потом `WSClient.Run` (private) — бесконечный цикл с реконнектом и backoff'ом.
3. WS-сообщения от Bybit:
   - **Private** `WSClient.dispatch`:
     - `topic=wallet` → `hub.ApplyWallet` (перезапись, но `marginMode` сохраняется из старого Wallet если в новом пусто — WS-wallet топик не несёт marginMode). Между WS-event и REST-поллом разница в шапке несущественна, но immediate-path даёт мгновенную реакцию на сделку.
     - `topic=position` → `hub.ApplyPositions`. Для каждой позиции из дельты:
       - если `Category` пуст — дефолт через `categoryFromSymbol` (дефис → `option`, иначе `linear`). Без этого новый символ не попадал в `Symbols("linear")` и public-WS на него не подписывался;
       - `size == "0"` → удаляем из map по ключу `category|symbol|positionIdx`;
       - позиция уже в map → `mergePosition`: пустые поля в дельте сохраняют старые значения (Bybit шлёт неизменившиеся поля как `""`, иначе их затирали бы); `SettleCoin`/`Category` тоже сохраняются через merge;
       - новой позиции с пустым size — игнорируем (partial update без known позиции).
       Дёргается `recomputeWalletLocked` (теперь только `totalPerpUPL`), бродкастится `{type:"positions"}` + `{type:"wallet"}`. Если изменился набор символов — `onPositionsChanged` синкает подписки public-WS.
     - сервисные ответы `op=auth/subscribe/pong` логируются если `success=false`.
   - **Public** (`linear` или `option`) `WSPublicClient.dispatch`:
     - `topic=tickers.<symbol>` → берём `markPrice` из payload'а → `hub.ApplyMarkPrice(c.category, symbol, mark)`.
     - `op=subscribe success=false` → парсим `args`, снимаем символы с `c.active` (иначе залипало до реконнекта).
4. `hub.ApplyMarkPrice(category, symbol, mark)`:
   - находит позиции с этим `Symbol`+`Category`, обновляет `markPrice`, пересчитывает `unrealisedPnl = (mark − avg) × size` (знак по `Side`) и `positionValue = size × mark` для linear-математики.
   - дёргает `recomputeWalletLocked` — это теперь упрощённая функция, считает только `wallet.totalPerpUPL = Σ position.unrealisedPnl`. Per-coin `equity`/`usdValue`/`unrealisedPnl` и `totalEquity` мы НЕ трогаем — они приходят авторитетными от Bybit через `pollWallet` каждые 2с (приложение считает их с учётом bonus, IM-локов, спот-цены не-стейбл монет; реплицировать локально нерально).
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

**Что сделано в этой сессии (2026-05-11, итерация 3) — гибрид REST-поллинга, freeze-fix:**

Контекст: у итерации 2 был баг расхождения шапки — `Total Equity` не совпадал с Bybit-app, потому что мы серверно собирали `wallet`-агрегаты из позиций (через `recomputeWalletLocked`), а Bybit считает их с учётом bonus / IM-локов / спот-цены не-стейбл монет — реплицировать всё это локально расходилось. Плюс отдельный баг: после открытия позиции на новом символе она «застывала» (mark/PnL не тикали) до перезапуска сервиса.

*Backend (`internal/bybit/client.go`):*
- **`pollWallet` (2с)** — новая горутина, `/v5/account/wallet-balance` → `hub.ApplyWallet`. Это теперь главный источник для `totalEquity`/`totalWalletBalance`/`totalAvailableBalance` и per-coin полей. Числа точно сходятся с Bybit-app в пределах 2с лага.
- **`pollPositions` (10с)** — safety-net. Перечитывает все 4 (linear/USDT, linear/USDC, option/USDT, option/USDC), зовёт `ApplyPositions`, и **унконсиционно** пинает `SetSymbols` на обоих public-стримах — это триггерит `syncSubs` и резабит то, что Bybit мог отвергнуть. Заодно перетегает `Category` правильно (REST-контекст), даёт baseline для `liqPrice`/`avgPrice`.

*Backend (`internal/bybit/ws_public.go`):*
- **`syncSubs` теперь пессимистичен** — `c.active` обновляется ТОЛЬКО после успешного send per-batch. До этого выставлялся оптимистично, и если Bybit отказал в subscribe (`op success=false`) или send тихо проваливался, `c.active` залипал с «claim'ом» подписки, которой нет; следующие `syncSubs` ничего не делали (diff пустой). **Это была главная причина freeze-on-open** — лечилось только реконнектом. Восстановление логично: после неуспеха символ остаётся в `desired` но не в `active`, следующий notify (или `pollPositions`-пинок) попробует снова.
- **dispatch обрабатывает `op=subscribe success=false`** — парсим `m.Args`, срезаем префикс `tickers.`, удаляем из `c.active`. Без этого пришлось бы ждать реконнект для очистки claim'а.

*Backend (`internal/hub/hub.go`):*
- **`recomputeWalletLocked` упрощён** — больше не трогает per-coin поля и `totalEquity`. Считает только `totalPerpUPL = Σ position.unrealisedPnl`. Bybit canonical через `pollWallet` побеждает наш расчёт. **Сайд-эффект: фикс залипания PnL в шапке после закрытия позиции** — раньше при удалении последней USDT-позиции `coinPnl["USDT"]` не существовал, цикл делал `continue`, `c.UnrealisedPnl` оставался от старого значения. Теперь сумма по позициям тривиально даёт 0.
- **Убрана `settleCoinFromSymbol`** — больше не нужна, эвристика по суффиксу никем не вызывается. `Position.SettleCoin` оставлен (поле в JSON-снапшоте + тег из REST-контекста), но не драйвит логику.
- **Добавлен `categoryFromSymbol`** — дефис в символе → `option`, иначе `linear`. Применяется в `ApplyPositions` для НОВЫХ позиций когда WS-private прислал дельту без `Category` (а это бывает). Без этого `Symbols("linear")` фильтр отбрасывал новый символ → public-WS на него не подписывался → второй уровень freeze-on-open.

*Не трогали:* фронт (`web/`), nginx, systemd, конфиг. WS-фрейм контракт и поля JSON — те же.

*Что проверено в проде:* `Total Equity` синхронизирован с Bybit-app. `Wallet Balance` тоже совпадает (юзер подтвердил после изначального сомнения). После закрытия позиции PnL в шапке моментально обнуляется. Freeze-on-open остаётся в фокусе — баг был воспроизведён ещё после первого фикса (category fallback), и тогда нашли корневую причину в `syncSubs` (пессимизм) — это уже задеплоено, ждём подтверждения от юзера на новой сделке.

*Что НЕ доделано в этой сессии:*
- Изменения **ещё не закоммичены** в git. Будут серией: hybrid REST poll, syncSubs pessimism, category fallback, recompute simplification.

**Что сделано в этой сессии (2026-05-12) — Margin column + mobile badge layout:**

*Бэкенд (`internal/hub/hub.go`):*
- Добавлено поле `Position.PositionIM string` (json `positionIM`) + проброс через `mergePosition`. Bybit V5 отдаёт это поле в `/v5/position/list` и WS-private `position` — раньше мы его игнорили при анмаршалле.

*Фронт (`web/src/lib/types.ts`, `web/src/lib/components/PositionsTab.svelte`, `web/src/lib/mockSeed.ts`):*
- Колонка «Margin» (моб + десктоп) теперь имеет 3-уровневый fallback:
  1. `positionIM` от Bybit (точная IM с учётом IM-ladder/tier; для перпов и SHORT опционов)
  2. `|positionValue| / leverage` (если IM пустой но leverage есть)
  3. `~|positionValue|` курсивом+muted с подсказкой (LONG опционы — Bybit для них `positionIM=""`, т.к. premium уже оплачен авансом, маржи как таковой нет; показываем текущую справедливую стоимость премии)
  4. `—` если совсем ничего
- Mobile-карточка реструктурирована: row1 = symbol + UPL (с `truncate`/`shrink-0`), row2 = badge на своей строке, row3 = grid 2×2. Раньше длинные опционные тикеры + бэдж + PnL впритык упирались.
- В моки досыпан `positionIM` чтобы TS-сборка не падала.

*Почему `Math.abs(positionValue)`:* Bybit для опционных шортов возвращает `positionValue` со знаком минус (short option = liability). Наш `ApplyMarkPrice` пересчитывает на `size×mark` (всегда +), создавая гонку: WS-private пишет `-X`, mark-тик пишет `+X` → мигание в UI и broadcast штормит. Abs в UI съедает мигание; в стейте знак продолжает скакать (можно почистить отдельно если приспичит).

**Открытые вопросы:**
1. ✅ `Total Equity` совпадает с Bybit-app после перехода на REST-полл wallet (`pollWallet` 2с).
2. ✅ `Wallet Balance` тоже сошёлся — `totalWalletBalance` идёт от Bybit как есть.
3. `Liq Price` теперь обновляется раз в 10с через `pollPositions` (baseline от Bybit) — но не sub-second. Реал-тайм liqPrice требовал бы серверной репликации Bybit-модели; не делаем.
4. Inverse-перпы и опции вне USDT/USDC settle не поддержаны — у друга их нет.
5. Cell-flash на апдейте — всё ещё в TODO. Mark/PnL/positionValue тикают, можно вешать.
6. Retry-цикл для REST-снапшота на старте — одна попытка с 20с timeout, при флапе snapshot тихо роняется. С появлением `pollWallet` (2с) и `pollPositions` (10с) это менее критично — следующий полл подтянет.
7. UI настроек для смены API-ключей через дашборд — пока ssh+systemctl restart.
8. Подтвердить отсутствие freeze-on-open на свежей сделке после деплоя этой итерации.

## Возможные расширения (если попросят)

- Добавить settleCoin'ы в конфиг (сейчас захардкожены `USDT`+`USDC` для linear- и option-снапшота; для других settle нужно расширять `client.go::snapshot`).
- Inverse-перпы (`category=inverse`): отдельный REST-pull, формула PnL `(1/avg − 1/mark) × size` (не линейная), и третий public-WS на `/v5/public/inverse`.
- Rate limit на `/api/login` сверх bcrypt-задержки.
- Username при логине, если потребуется отображать в UI / логах.
- Cell-flash на апдейте (UX-приятность; ключевое — `markPrice`/`unrealisedPnl` уже тикают, есть на чём вешать).
- Юзерская сортировка / фильтры в таблицах (по PnL, по размеру). Серверный `sortedPositions` даёт стабильный baseline.
- Тостеры/уведомления на ликвидацию или резкое движение PnL.
- Approx `totalAvailableBalance` сервёрно — обсуждали и отвергли (расходилось бы с Bybit-app на пару %; точная формула зависит от cross/iso/portfolio + IM/MMR ladders). Если надо — `equity − Σ(positionValue/leverage)` как старт.
