import asyncio
import hashlib
import hmac
import json
import time

import websockets

with open("config.json", encoding="utf-8") as f:
    import json as _j
    cfg = _j.load(f)

API_KEY = cfg["api_key"]
API_SECRET = cfg["api_secret"]


async def probe(url: str):
    print(f"\n=== {url}")
    expires = int(time.time() * 1000) + 10000
    msg = f"GET/realtime{expires}"
    sig = hmac.new(API_SECRET.encode(), msg.encode(), hashlib.sha256).hexdigest()
    try:
        async with websockets.connect(url, open_timeout=10, close_timeout=2) as ws:
            await ws.send(json.dumps({"op": "auth", "args": [API_KEY, expires, sig]}))
            resp = await asyncio.wait_for(ws.recv(), timeout=8)
            print(f"  AUTH RESPONSE: {resp}")
    except Exception as e:
        print(f"  ERROR: {type(e).__name__}: {e}")


async def main():
    await probe("wss://stream-testnet.bybit.com/v5/private")
    await probe("wss://stream-testnet.bybit.eu/v5/private")


asyncio.run(main())
