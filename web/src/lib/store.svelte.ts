import type { Position, Snapshot, Status, Wallet, WSFrame } from "./types";

type AppState = {
  wallet: Wallet | null;
  positions: Position[];
  status: Status;
  authed: boolean;
  wsConnected: boolean;
  demo: boolean;
};

export const app: AppState = $state({
  wallet: null,
  positions: [],
  status: { connected: false, updatedAt: "" },
  authed: false,
  wsConnected: false,
  demo: false,
});

let ws: WebSocket | null = null;
let reconnectTimer: number | null = null;
let backoff = 1000;

export function applySnapshot(s: Snapshot) {
  app.wallet = s.wallet;
  app.positions = s.positions ?? [];
  app.status = s.status;
}

function applyFrame(f: WSFrame) {
  switch (f.type) {
    case "snapshot":
      applySnapshot(f.data);
      break;
    case "wallet":
      app.wallet = f.data;
      break;
    case "positions":
      app.positions = f.data ?? [];
      break;
    case "status":
      app.status = f.data;
      break;
  }
}

export function connectWS() {
  if (ws && (ws.readyState === WebSocket.CONNECTING || ws.readyState === WebSocket.OPEN)) {
    return;
  }
  const proto = location.protocol === "https:" ? "wss" : "ws";
  ws = new WebSocket(`${proto}://${location.host}/api/ws`);
  ws.onopen = () => {
    app.wsConnected = true;
    backoff = 1000;
  };
  ws.onmessage = (ev) => {
    try {
      const f = JSON.parse(ev.data) as WSFrame;
      applyFrame(f);
    } catch (e) {
      console.error("ws parse error", e);
    }
  };
  ws.onclose = () => {
    app.wsConnected = false;
    if (reconnectTimer) clearTimeout(reconnectTimer);
    reconnectTimer = window.setTimeout(connectWS, backoff);
    backoff = Math.min(backoff * 2, 30000);
  };
  ws.onerror = () => {
    ws?.close();
  };
}

export function disconnectWS() {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  ws?.close();
  ws = null;
  app.wsConnected = false;
}
