// See ARCHITECTURE.md §10.3 — Hopeful tab layout
// Leaders (isHopeful:true, isSympathy:false) above divider,
// sympathy plays (isSympathy:true) below with "SYMPATHY PLAYS" label.
// Data is passed in via props — App.tsx owns the data source (real or dev).

import type { StockObject } from '../types';
import StockRow from './StockRow';

interface HopefulFeedProps {
  leaders: StockObject[];
  sympathy: StockObject[];
  searchQuery: string;
  devMode: boolean;
}

// Column header style — matches LiveFeed exactly.
const headerStyle: React.CSSProperties = {
  fontSize: '10px',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  color: '#9A9590',
};

export default function HopefulFeed({
  leaders,
  sympathy,
  searchQuery,
  devMode,
}: HopefulFeedProps): JSX.Element {
  // Filter by search query — case-insensitive ticker match.
  const query = searchQuery.trim().toUpperCase();
  const filteredLeaders = query
    ? leaders.filter((s) => s.ticker.toUpperCase().includes(query))
    : leaders;
  const filteredSympathy = query
    ? sympathy.filter((s) => s.ticker.toUpperCase().includes(query))
    : sympathy;

  // Empty state: search returned nothing across both sections.
  if (query && filteredLeaders.length === 0 && filteredSympathy.length === 0) {
    return (
      <div className="flex items-center justify-center py-16 text-hint text-sm">
        No stocks found
      </div>
    );
  }

  return (
    <div className="bg-bg">
      {/* Column header row — same layout as LiveFeed */}
      <div className="flex items-center px-4 py-2 border-b border-border bg-surface">
        <div className="shrink-0" style={{ width: '24px', ...headerStyle }}>#</div>
        <div className="shrink-0" style={{ width: '80px', ...headerStyle }}>Ticker</div>
        <div className="flex-1 px-2" style={headerStyle}>Reason</div>
        <div className="shrink-0 text-right" style={{ width: '82px', ...headerStyle }}>Price</div>
        <div className="shrink-0 text-right" style={{ width: '58px', ...headerStyle }}>R.Vol</div>
        <div className="shrink-0 text-right" style={{ width: '84px', ...headerStyle }}>Change</div>
      </div>

      {/* Section 1 — Leaders (no label, just ranked rows) */}
      {filteredLeaders.length > 0 ? (
        filteredLeaders.map((stock, i) => (
          <StockRow
            key={stock.ticker}
            stock={stock}
            rank={i + 1}
            highlight={!!query}
            devMode={devMode}
          />
        ))
      ) : (
        <div className="flex items-center justify-center py-10 text-hint text-sm">
          No Hopeful stocks active today
        </div>
      )}

      {/* Divider — "SYMPATHY PLAYS" label in muted uppercase, thin border above + below */}
      <div
        className="px-4 py-1.5 border-t border-b border-border"
        style={{
          fontSize: '10px',
          textTransform: 'uppercase',
          letterSpacing: '0.06em',
          color: '#9A9590',
        }}
      >
        Sympathy plays
      </div>

      {/* Section 2 — Sympathy plays */}
      {filteredSympathy.length > 0 ? (
        filteredSympathy.map((stock, i) => (
          <StockRow
            key={stock.ticker}
            stock={stock}
            rank={filteredLeaders.length + i + 1}
            highlight={!!query}
            devMode={devMode}
          />
        ))
      ) : (
        <div className="flex items-center justify-center py-10 text-hint text-sm">
          No sympathy plays detected
        </div>
      )}
    </div>
  );
}
