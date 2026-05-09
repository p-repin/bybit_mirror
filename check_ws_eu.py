import asyncio
import hashlib
import hmac
import json
import time

import websockets

with open("config.json", encoding="utf-8") as f:
    cfg = json.load(f)

API_KEY = cfg["api_key"]
API_SECRET = cfg["api_secret"]
URL = "wss://stream-testnet.bybit.eu/v5/private"


async def probe(n: int):
    expires = int(time.time() * 1000) + 10000
    msg = f"GET/realtime{expires}"
    sig = hmac.new(API_SECRET.encode(), msg.encode(), hashlib.sha256).hexdigest()
    t0 = time.monotonic()
    try:
        async with websockets.connect(URL, open_timeout=10, close_timeout=2) as ws:
            handshake_t = time.monotonic() - t0
            await ws.send(json.dumps({"op": "auth", "args": [API_KEY, expires, sig]}))
            resp = await asyncio.wait_for(ws.recv(), timeout=5)
            print(f"  [{n}] OK handshake={handshake_t:.2f}s auth={resp}")
    except Exception as e:
        print(f"  [{n}] FAIL after {time.monotonic()-t0:.2f}s: {type(e).__name__}: {e}")


async def main():
    print(f"Probing {URL} 6 times...")
    for i in range(1, 7):
        await probe(i)
        await asyncio.sleep(1)


asyncio.run(main())
