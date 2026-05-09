# bybit-service

Веб-сервис, показывающий состояние **одного** Bybit-аккаунта **нескольким** пользователям, входящим под одним общим паролем. Бэкенд держит постоянное соединение с Bybit private WebSocket, хранит актуальный стейт (wallet + positions) в памяти и стримит обновления подключённым клиентам.

Фронт лежит в `web/` (Svelte 5 + Vite + Tailwind v4 + shadcn-style тёмная zinc-палитра), в проде собирается в статичный `dist/` и хостится nginx-ом, который заодно проксирует `/api/*` в Go-сервис.

## Архитектура

```
Bybit private WS ──┐
                   ├──► hub (in-memory state) ──► browser clients (WS)
Bybit REST (init) ─┘                          └─► /api/snapshot (HTTP)
```

- Один процесс держит одно WS-соединение с Bybit и реплицирует его всем браузерным клиентам.
- Стейт никогда не персистится — это «зеркало» текущего состояния аккаунта.
- На старте делается REST-снапшот (`wallet-balance` + `position/list` для linear USDT/USDC), затем подписка на WS-топики `wallet` и `position` приносит дельты.

## Карта кода

```
cmd/genpass/main.go         — утилита: пароль → bcrypt-хеш + session_secret (hex)
cmd/server/main.go          — точка входа: load config → hub → bybit.Client → http.Server
internal/config/config.go   — JSON-конфиг и его валидация
internal/auth/auth.go       — bcrypt-проверка + HMAC-подписанный stateless cookie
internal/bybit/sign.go      — HMAC-SHA256 (для REST-подписи и WS-auth)
internal/bybit/rest.go      — RESTClient: WalletBalance, Positions (с пагинацией)
internal/bybit/ws.go        — WSClient: dial, auth, subscribe, ping/20s, reconnect 1→30s
internal/bybit/client.go    — оркестратор: REST snapshot → WS Run
internal/server/server.go   — http-роуты, login/logout/snapshot
internal/server/ws.go       — апгрейд /api/ws, ping/30s, pong-deadline 70s
internal/hub/hub.go         — состояние (Wallet, Positions map, Status), broadcast клиентам

web/                        — Svelte 5 + Vite + Tailwind v4 (shadcn-стиль)
web/src/App.svelte          — root: tryBoot → snapshot/login routing
web/src/lib/api.ts          — login / logout / snapshot HTTP-клиент
web/src/lib/store.svelte.ts — глобальный $state ($state-runes file), connectWS с auto-reconnect
web/src/lib/types.ts        — типы Wallet/Position/Status/WSFrame, зеркало internal/hub/hub.go
web/src/lib/mockSeed.ts     — dev-only: фейковые wallet+positions для UI-итераций
web/src/lib/components/     — LoginScreen, Dashboard, AssetsTab, PositionsTab, StatusDot
```

## Поток данных

1. `cmd/server/main.go` стартует:
   - грузит конфиг,
   - создаёт `hub.Hub`,
   - инициализирует `auth.Manager`,
   - запускает `bybit.Client.Run(ctx)` в горутине,
   - запускает `http.Server`.
2. `bybit.Client.Run`:
   - сначала `snapshot()` — REST-вызовы `/v5/account/wallet-balance` и `/v5/position/list` (linear, settleCoin=USDT и USDC), результат заливается в hub через `ApplyWallet` / `ApplyPositions`,
   - потом `WSClient.Run` — бесконечный цикл с реконнектом и backoff'ом.
3. WS-сообщения от Bybit разбираются в `WSClient.dispatch`:
   - `topic=wallet` → `hub.ApplyWallet` (полная замена) → broadcast `{type:"wallet"}`,
   - `topic=position` → `hub.ApplyPositions` (мердж в map по ключу `category|symbol|positionIdx`, удаление при `size=="0"`) → broadcast `{type:"positions"}`,
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

Сервис рассчитан жить за **nginx reverse proxy на том же origin**:

```nginx
location /api/ {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $connection_upgrade;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
}
location / {
    root /var/www/bybit-front;
    try_files $uri /index.html;
}
```

`upgrader.CheckOrigin` сейчас возвращает `true` — фильтрация origin делается на nginx.

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

## Текущий статус (2026-05-09)

End-to-end pipeline проверен и работает. Юзер на Bybit EU (`testnet.bybit.eu`, `region: "eu"`). Реальный перевод USDC из Funding в Unified пролетел по WS-топику `wallet`, прилёг в hub, отображается в `/api/snapshot` и в UI.

**Что сделано в этой сессии:**
- Бэк: добавлено поле `region` (см. таблицу конфига); WS dial timeout с 15с до 8с (EU Akamai-edge флапает, retry-цикл с backoff 1→30с проскакивает за 2-3 попытки).
- Фронт: скаффолд Vite + Svelte 5 + TS + Tailwind v4 + shadcn-style тёмная zinc-палитра + Geist шрифт. Две вкладки (Активы / Позиции), LoginScreen, реактивный $state-store с auto-reconnect WS, dev-only mock-сидер для UI-итераций.
- В корне есть `check_key.py` (CCXT REST), `check_ws.py`/`check_ws_eu.py` (Python websockets) для повторной диагностики, если Bybit-сторона снова закапризничает.

**Что НЕ проверено напрямую:**
- WS-событие `position` — Bybit EU testnet требует KYC для разблокировки деривативов. Код-путь идентичен `wallet` (см. `internal/bybit/ws.go:147-162`), который работает. Проверится естественно, когда друг подключит mainnet-ключ.

**Что дальше (открыто на следующую сессию):**
1. Адаптив под мобилку/планшет: на `< 768px` — карточный вид для позиций вместо таблицы, скрытие неважных колонок (`hidden md:table-cell`), sticky-колонка «Символ» при горизонтальном скролле.
2. Cell-flash на апдейте (мигание ячейки при изменении PnL/цены — UX-приятность, как у бирж).
3. Деплой: `npm run build` → `web/dist`, nginx-конфиг из секции «Деплой», systemd unit для Go-бинаря.
4. Подключение mainnet-ключа друга — заменить `api_key`/`api_secret` в `config.json`, поставить `environment: "mainnet"` (и `region` под аккаунт).

## Возможные расширения (если попросят)

- Добавить settleCoin'ы в конфиг (сейчас захардкожены `USDT`+`USDC` для linear-снапшота).
- Фильтр категорий позиций на бэке (сейчас WS-топик `position` без фильтра — теоретически придут все, что подписывается под аккаунтом).
- Rate limit на `/api/login` сверх bcrypt-задержки.
- Username при логине, если потребуется отображать в UI / логах.
- Адаптив фронта под мобилку (см. «Что дальше»).
- Cell-flash на апдейте (см. «Что дальше»).
- Сортировка / фильтры в таблицах (по PnL, по размеру).
- Тостеры/уведомления на ликвидацию или резкое движение PnL.
