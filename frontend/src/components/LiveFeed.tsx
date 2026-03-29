// See ARCHITECTURE.md §10.1 — ranked stock list, re-renders every 1s
// Uses useSectorFeed hook for 1s polling, renders StockRow per stock.

import { useSectorFeed } from '../hooks/useSectorFeed';
import StockRow from './StockRow';

interface LiveFeedProps {
  sector: string;
}

export default function LiveFeed({ sector }: LiveFeedProps): JSX.Element {
  const stocks = useSectorFeed(sector);

  if (stocks.length === 0) {
    return (
      <div className="flex items-center justify-center py-16 text-hint text-sm">
        No stocks in {sector} right now
      </div>
    );
  }

  return (
    <div className="bg-bg">
      {/* Column headers */}
      <div className="flex items-center gap-3 px-4 py-2 border-b border-border bg-surface text-xs text-muted">
        <div className="flex-1">Ticker</div>
        <div className="w-20 text-right">Price</div>
        <div className="w-20 text-right">Change</div>
        <div className="w-14 text-right">Z</div>
        <div className="w-16 text-right">RVol</div>
      </div>

      {stocks.map((stock) => (
        <StockRow key={stock.ticker} stock={stock} />
      ))}
    </div>
  );
}
