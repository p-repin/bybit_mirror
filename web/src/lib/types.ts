// Mirrors internal/hub/hub.go — JSON tags must match.

export interface WalletCoin {
  coin: string;
  equity: string;
  walletBalance: string;
  usdValue: string;
  availableToWithdraw: string;
  unrealisedPnl: string;
  cumRealisedPnl: string;
}

export interface Wallet {
  accountType: string;
  totalEquity: string;
  totalWalletBalance: string;
  totalAvailableBalance: string;
  totalMarginBalance: string;
  totalPerpUPL: string;
  coin: WalletCoin[];
  marginMode?: string;
}

export interface Position {
  category: string;
  symbol: string;
  side: string;
  size: string;
  avgPrice: string;
  positionValue: string;
  positionIM: string;
  unrealisedPnl: string;
  cumRealisedPnl: string;
  leverage: string;
  liqPrice: string;
  markPrice: string;
  positionIdx: number;
  tradeMode?: number;
  takeProfit: string;
  stopLoss: string;
  trailingStop: string;
  createdTime: string;
  updatedTime: string;
}

export interface Status {
  connected: boolean;
  lastError?: string;
  updatedAt: string;
}

export interface Snapshot {
  wallet: Wallet | null;
  positions: Position[];
  status: Status;
}

export type WSFrame =
  | { type: "snapshot"; data: Snapshot }
  | { type: "wallet"; data: Wallet }
  | { type: "positions"; data: Position[] }
  | { type: "status"; data: Status };
