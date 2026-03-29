// See ARCHITECTURE.md §9.3 — stock object schema returned by the Python API.
// Shared across all hooks and components. Strict types, no `any`.

export interface StockObject {
  ticker: string;
  sector: string;
  price: number;
  changePercent: number;
  zScore: number;
  relVol: number;
  isHopeful: boolean;
  isSympathy: boolean;
  parent: string | null;
  reason: string;
  reasonStatus: 'ready' | 'generating' | 'unavailable';
}

// See ARCHITECTURE.md §10.3 — hopeful endpoint returns leaders + sympathy split.
export interface HopefulResponse {
  leaders: StockObject[];
  sympathy: StockObject[];
}

// See ARCHITECTURE.md §9.1 — GET /stocks/{ticker}/reason response.
export interface ReasonResponse {
  reason: string;
  status: 'ready' | 'generating' | 'unavailable';
}
