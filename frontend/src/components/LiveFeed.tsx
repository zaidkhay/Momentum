// See ARCHITECTURE.md §10.1 — ranked stock list, re-renders every 1s
// Data is passed in via props — App.tsx owns the data source (real or dev).

import type { StockObject } from '../types';
import StockRow from './StockRow';

interface LiveFeedProps {
  stocks: StockObject[];
  searchQuery: string;
  devMode: boolean;
}

// Column header style — 10px, uppercase, letter-spacing 0.04em, color #9A9590.
const headerStyle: React.CSSProperties = {
  fontSize: '10px',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  color: '#9A9590',
};

// Loading skeleton — 5 rows of pulsing placeholder bars shown during initial fetch.
function Skeleton(): JSX.Element {
  return (
    <>
      {Array.from({ length: 5 }).map((_, i) => (
        <div key={i} className="flex items-center px-4 py-3 border-b border-border animate-pulse">
          <div className="shrink-0" style={{ width: '24px' }}>
            <div className="h-3 w-4 rounded bg-border" />
          </div>
          <div className="shrink-0" style={{ width: '80px' }}>
            <div className="h-3 w-12 rounded bg-border" />
          </div>
          <div className="flex-1 px-2">
            <div className="h-3 w-3/4 rounded bg-border" />
          </div>
          <div className="shrink-0 text-right" style={{ width: '82px' }}>
            <div className="h-3 w-14 rounded bg-border ml-auto" />
          </div>
          <div className="shrink-0 text-right" style={{ width: '58px' }}>
            <div className="h-3 w-8 rounded bg-border ml-auto" />
          </div>
          <div className="shrink-0 text-right" style={{ width: '84px' }}>
            <div className="h-3 w-12 rounded bg-border ml-auto" />
          </div>
        </div>
      ))}
    </>
  );
}

export default function LiveFeed({
  stocks,
  searchQuery,
  devMode,
}: LiveFeedProps): JSX.Element {
  // Filter stocks by search query — case-insensitive ticker match.
  const query = searchQuery.trim().toUpperCase();
  const filtered = query
    ? stocks.filter((s) => s.ticker.toUpperCase().includes(query))
    : stocks;

  // Empty state: search returned nothing.
  if (query && filtered.length === 0) {
    return (
      <div className="flex items-center justify-center py-16 text-hint text-sm">
        No stocks found
      </div>
    );
  }

  // Empty state: no data and no search — waiting for market data.
  // In devMode data is always present, so this only shows in production.
  if (!devMode && stocks.length === 0 && !query) {
    return (
      <div className="bg-bg">
        {/* Column header row — background #F5F0E4 */}
        <div className="flex items-center px-4 py-2 border-b border-border bg-surface">
          <div className="shrink-0" style={{ width: '24px', ...headerStyle }}>#</div>
          <div className="shrink-0" style={{ width: '80px', ...headerStyle }}>Ticker</div>
          <div className="flex-1 px-2" style={headerStyle}>Reason</div>
          <div className="shrink-0 text-right" style={{ width: '82px', ...headerStyle }}>Price</div>
          <div className="shrink-0 text-right" style={{ width: '58px', ...headerStyle }}>R.Vol</div>
          <div className="shrink-0 text-right" style={{ width: '84px', ...headerStyle }}>Change</div>
        </div>
        <Skeleton />
        <div className="flex items-center justify-center py-8 text-hint text-sm">
          Waiting for market data...
        </div>
      </div>
    );
  }

  return (
    <div className="bg-bg">
      {/* Column header row — background #F5F0E4 */}
      <div className="flex items-center px-4 py-2 border-b border-border bg-surface">
        <div className="shrink-0" style={{ width: '24px', ...headerStyle }}>#</div>
        <div className="shrink-0" style={{ width: '80px', ...headerStyle }}>Ticker</div>
        <div className="flex-1 px-2" style={headerStyle}>Reason</div>
        <div className="shrink-0 text-right" style={{ width: '82px', ...headerStyle }}>Price</div>
        <div className="shrink-0 text-right" style={{ width: '58px', ...headerStyle }}>R.Vol</div>
        <div className="shrink-0 text-right" style={{ width: '84px', ...headerStyle }}>Change</div>
      </div>

      {filtered.map((stock, i) => (
        <StockRow
          key={stock.ticker}
          stock={stock}
          rank={i + 1}
          highlight={!!query}
          devMode={devMode}
        />
      ))}
    </div>
  );
}
