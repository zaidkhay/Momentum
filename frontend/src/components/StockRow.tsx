// See ARCHITECTURE.md §10.1 — ticker, reason, price, rvol, change%
// Rule 6 (WINDSURF.md): all numbers must use toFixed(2) — no raw floats
// See ARCHITECTURE.md §10.2 — flash animation on price change, 350ms

import { useEffect, useRef, useState } from 'react';
import type { StockObject } from '../types';
import { useReason } from '../hooks/useReason';

interface StockRowProps {
  stock: StockObject;
}

export default function StockRow({ stock }: StockRowProps): JSX.Element {
  const prevPriceRef = useRef<number>(stock.price);
  const [flashClass, setFlashClass] = useState<string>('');

  // Use the reason hook only when the inline reason isn't ready yet.
  const { reason: polledReason, status: polledStatus } = useReason(stock.ticker);

  // Prefer the reason already on the stock object (from sector hydration),
  // fall back to the polled reason from useReason.
  const reason = stock.reason || polledReason;
  const reasonStatus = stock.reasonStatus === 'ready' ? 'ready' : polledStatus;

  // See ARCHITECTURE.md §10.2 — flash-up / flash-down on price change.
  useEffect(() => {
    const prev = prevPriceRef.current;
    if (stock.price > prev) {
      setFlashClass('flash-up');
    } else if (stock.price < prev) {
      setFlashClass('flash-down');
    }
    prevPriceRef.current = stock.price;

    const timer = setTimeout(() => setFlashClass(''), 350);
    return () => clearTimeout(timer);
  }, [stock.price]);

  const isUp = stock.changePercent >= 0;
  const changeColor = isUp ? 'text-up' : 'text-down';
  const changeBg = isUp ? 'bg-up-bg' : 'bg-down-bg';

  return (
    <div
      className={`flex items-center gap-3 px-4 py-3 border-b border-border ${flashClass}`}
    >
      {/* Ticker + reason */}
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2">
          <span className="font-semibold text-primary text-sm">
            {stock.ticker}
          </span>
          {stock.isHopeful && (
            <span className="text-[10px] font-medium px-1.5 py-0.5 rounded bg-accent text-secondary">
              HOPEFUL
            </span>
          )}
          {stock.isSympathy && stock.parent && (
            <span className="text-[10px] font-medium px-1.5 py-0.5 rounded bg-accent text-secondary">
              via {stock.parent}
            </span>
          )}
        </div>
        <p className="text-xs text-muted truncate mt-0.5">
          {reasonStatus === 'ready'
            ? reason
            : reasonStatus === 'generating'
              ? 'Analyzing...'
              : ''}
        </p>
      </div>

      {/* Price */}
      <div className="text-right w-20 shrink-0">
        <span className="text-sm font-medium text-primary">
          ${stock.price.toFixed(2)}
        </span>
      </div>

      {/* Change % badge */}
      <div className="w-20 shrink-0 text-right">
        <span
          className={`inline-block text-xs font-semibold px-2 py-1 rounded ${changeBg} ${changeColor}`}
        >
          {isUp ? '+' : ''}
          {stock.changePercent.toFixed(2)}%
        </span>
      </div>

      {/* Z-score */}
      <div className="w-14 shrink-0 text-right">
        <span className="text-xs text-secondary">
          Z {stock.zScore.toFixed(2)}
        </span>
      </div>

      {/* Relative volume */}
      <div className="w-16 shrink-0 text-right">
        <span className="text-xs text-secondary">
          {stock.relVol.toFixed(2)}x vol
        </span>
      </div>
    </div>
  );
}
