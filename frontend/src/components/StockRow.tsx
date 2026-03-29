// See ARCHITECTURE.md §10.1 — ticker, reason, price, rvol, change%
// Rule 6 (WINDSURF.md): all numbers must use toFixed(2) — no raw floats
// See ARCHITECTURE.md §10.2 — flash animation on price change, 350ms

import { useEffect, useRef, useState } from 'react';
import type { StockObject } from '../types';
import { useReason } from '../hooks/useReason';

interface StockRowProps {
  stock: StockObject;
  rank?: number;       // optional # column
  highlight?: boolean; // true when search matches this row
  devMode?: boolean;   // when true, skip useReason — read from stock prop directly
}

// Badge inline style — shared by HOPEFUL and SYMPATHY badges.
// Uses exact hex values from spec: bg #EDE8DC, color #3A3A3A, border #E2DDD4.
const badgeStyle: React.CSSProperties = {
  background: '#EDE8DC',
  color: '#3A3A3A',
  border: '1px solid #E2DDD4',
  fontSize: '9px',
  borderRadius: '3px',
  padding: '1px 5px',
  lineHeight: '14px',
  whiteSpace: 'nowrap',
};

export default function StockRow({
  stock,
  rank,
  highlight = false,
  devMode = false,
}: StockRowProps): JSX.Element {
  // useRef stores previous price so we can detect direction changes and
  // trigger flash-up / flash-down CSS animations on the row.
  const prevPriceRef = useRef<number>(stock.price);
  const [flashClass, setFlashClass] = useState<string>('');

  // In DEV_MODE, skip the useReason polling hook entirely — reason data
  // is baked into the stock prop and mutated by App.tsx for simulation.
  // In production mode, poll /stocks/{ticker}/reason every 2s.
  const polled = useReason(devMode ? '' : stock.ticker);

  // Determine displayed reason: prefer stock prop, fall back to polled data.
  const reason = devMode
    ? (stock.reason || '')
    : (stock.reason || polled.reason);
  const reasonStatus = devMode
    ? stock.reasonStatus
    : (stock.reasonStatus === 'ready' ? 'ready' : polled.status);

  // See ARCHITECTURE.md §10.2 — flash-up / flash-down on price change.
  // useEffect fires whenever stock.price changes. We compare to the
  // previous value stored in prevPriceRef to determine direction.
  useEffect(() => {
    const prev = prevPriceRef.current;
    if (stock.price > prev) {
      setFlashClass('flash-up');
    } else if (stock.price < prev) {
      setFlashClass('flash-down');
    }
    prevPriceRef.current = stock.price;

    // Clear the flash class after 350ms so the animation can re-trigger.
    const timer = setTimeout(() => setFlashClass(''), 350);
    return () => clearTimeout(timer);
  }, [stock.price]);

  // Change % color: positive → #1A6B3C, negative → #8B1A1A, zero → #9A9590.
  const changePctColor =
    stock.changePercent > 0
      ? '#1A6B3C'
      : stock.changePercent < 0
        ? '#8B1A1A'
        : '#9A9590';

  // Reason text color and content based on status.
  let reasonText: string;
  let reasonColor: string;
  let reasonItalic = false;
  if (reasonStatus === 'ready' && reason) {
    reasonText = reason;
    reasonColor = '#7A7772';
  } else if (reasonStatus === 'generating') {
    reasonText = 'Analyzing...';
    reasonColor = '#C0BAB2';
    reasonItalic = true;
  } else {
    reasonText = 'No catalyst found';
    reasonColor = '#C0BAB2';
  }

  return (
    <div
      className={`flex items-center px-4 py-2.5 border-b border-border ${flashClass} ${highlight ? 'bg-surface' : ''}`}
    >
      {/* # rank — 24px */}
      {rank !== undefined && (
        <div className="shrink-0 text-xs text-hint" style={{ width: '24px' }}>
          {rank}
        </div>
      )}

      {/* Ticker + badge — 80px */}
      <div className="shrink-0 flex items-center gap-1.5" style={{ width: '80px' }}>
        <span className="font-semibold text-primary text-sm leading-none">
          {stock.ticker}
        </span>
        {stock.isHopeful && !stock.isSympathy && (
          <span style={badgeStyle}>HOPEFUL</span>
        )}
        {stock.isSympathy && stock.parent && (
          <span style={badgeStyle}>SYMPATHY · {stock.parent}</span>
        )}
      </div>

      {/* Reason — flex-1 */}
      <div className="flex-1 min-w-0 px-2">
        <p
          className="text-xs truncate leading-tight"
          style={{
            color: reasonColor,
            fontStyle: reasonItalic ? 'italic' : 'normal',
          }}
        >
          {reasonText}
        </p>
      </div>

      {/* Price — 82px, right-aligned, monospace */}
      <div className="shrink-0 text-right" style={{ width: '82px' }}>
        <span className="text-sm text-primary" style={{ fontFamily: 'monospace' }}>
          ${stock.price.toFixed(2)}
        </span>
      </div>

      {/* R.Vol — 58px, right-aligned */}
      <div className="shrink-0 text-right" style={{ width: '58px' }}>
        <span className="text-xs text-secondary" style={{ fontFamily: 'monospace' }}>
          {stock.relVol.toFixed(1)}×
        </span>
      </div>

      {/* Change % — 84px, right-aligned, monospace, bold */}
      <div className="shrink-0 text-right" style={{ width: '84px' }}>
        <span
          className="text-xs font-bold"
          style={{ fontFamily: 'monospace', color: changePctColor }}
        >
          {stock.changePercent > 0 ? '+' : ''}
          {stock.changePercent.toFixed(2)}%
        </span>
      </div>
    </div>
  );
}
