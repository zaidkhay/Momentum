// See ARCHITECTURE.md §10.3 — Hopeful tab layout
// Leaders (isHopeful:true, isSympathy:false) above divider,
// sympathy plays (isSympathy:true) below, grouped by parent ticker.

import { useHopeful } from '../hooks/useHopeful';
import StockRow from './StockRow';
import type { StockObject } from '../types';

/**
 * Groups sympathy plays by their parent ticker.
 * Returns a Map preserving insertion order (first parent seen first).
 */
function groupByParent(sympathy: StockObject[]): Map<string, StockObject[]> {
  const groups = new Map<string, StockObject[]>();
  for (const stock of sympathy) {
    const parent = stock.parent ?? 'Unknown';
    const list = groups.get(parent) ?? [];
    list.push(stock);
    groups.set(parent, list);
  }
  return groups;
}

export default function HopefulFeed(): JSX.Element {
  const { leaders, sympathy } = useHopeful();

  const hasData = leaders.length > 0 || sympathy.length > 0;

  if (!hasData) {
    return (
      <div className="flex items-center justify-center py-16 text-hint text-sm">
        No Hopeful stocks right now
      </div>
    );
  }

  const sympathyGroups = groupByParent(sympathy);

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

      {/* Leaders section */}
      {leaders.length > 0 && (
        <div>
          <div className="px-4 py-1.5 bg-accent text-xs font-medium text-secondary">
            Leaders
          </div>
          {leaders.map((stock) => (
            <StockRow key={stock.ticker} stock={stock} />
          ))}
        </div>
      )}

      {/* Sympathy section — grouped by parent ticker */}
      {sympathy.length > 0 && (
        <div>
          <div className="px-4 py-1.5 bg-accent text-xs font-medium text-secondary border-t border-border">
            Sympathy Plays
          </div>
          {Array.from(sympathyGroups.entries()).map(([parent, stocks]) => (
            <div key={parent}>
              <div className="px-4 py-1 bg-surface text-[11px] text-muted">
                via {parent}
              </div>
              {stocks.map((stock) => (
                <StockRow key={stock.ticker} stock={stock} />
              ))}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
