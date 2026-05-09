import json
import sys
import ccxt

with open("config.json", encoding="utf-8") as f:
    cfg = json.load(f)

print(f"ccxt version: {ccxt.__version__}")
print(f"api_key:      {cfg['api_key'][:6]}...{cfg['api_key'][-4:]}")
print(f"environment:  testnet (sandbox mode)")
print()

ex = ccxt.bybit({
    "apiKey": cfg["api_key"],
    "secret": cfg["api_secret"],
    "enableRateLimit": True,
})
ex.set_sandbox_mode(True)

print(f"REST endpoint: {ex.urls['api']}")
print()

print(">>> fetch_time (no auth, just connectivity)")
try:
    t = ex.fetch_time()
    print(f"    server time: {t}")
except Exception as e:
    print(f"    ERROR: {e}")
    sys.exit(1)

print()
print(">>> fetch_balance (UNIFIED, requires auth)")
try:
    bal = ex.fetch_balance({"type": "unified"})
    print(f"    OK. Coins with balance:")
    for coin, info in (bal.get("total") or {}).items():
        if info and float(info) > 0:
            print(f"      {coin}: total={info}")
    if not any(float(v or 0) > 0 for v in (bal.get("total") or {}).values()):
        print("      (wallet empty, but auth succeeded)")
except Exception as e:
    print(f"    ERROR: {type(e).__name__}: {e}")

print()
print(">>> fetch_positions (linear)")
try:
    pos = ex.fetch_positions(params={"category": "linear", "settleCoin": "USDT"})
    print(f"    OK. {len(pos)} positions returned")
    for p in pos[:5]:
        print(f"      {p['symbol']} {p['side']} size={p['contracts']}")
except Exception as e:
    print(f"    ERROR: {type(e).__name__}: {e}")
