// See ARCHITECTURE.md §10.1 — wire SectorTabs, LiveFeed, HopefulFeed together
// DEV_MODE: set to true for visual testing outside market hours.
// Flip to false before production deployment.

import { useCallback, useEffect, useRef, useState } from 'react';
import type { CSSProperties } from 'react';
import SectorTabs from './components/SectorTabs';
import LiveFeed from './components/LiveFeed';
import HopefulFeed from './components/HopefulFeed';
import { useSectorFeed } from './hooks/useSectorFeed';
import { useHopeful } from './hooks/useHopeful';
import { DUMMY_STOCKS, DUMMY_HOPEFUL } from './dev/DummyData';
import type { StockObject, HopefulResponse } from './types';

// ── DEV_MODE toggle ──────────────────────────────────────────────────────────
// When true: uses dummy data with simulated price jitter and reason transitions.
// When false: polls the real Python FastAPI endpoints.
const DEV_MODE = true;

// ── Helper: format time as HH:MM:SS ─────────────────────────────────────────
function formatTime(d: Date): string {
  return d.toLocaleTimeString('en-US', { hour12: false });
}

// ── SVG magnifier icon for search bar (13×13) ───────────────────────────────
function SearchIcon(): JSX.Element {
  return (
    <svg
      width="13"
      height="13"
      viewBox="0 0 24 24"
      fill="none"
      stroke="#B0ABA3"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <circle cx="11" cy="11" r="8" />
      <line x1="21" y1="21" x2="16.65" y2="16.65" />
    </svg>
  );
}

export default function App(): JSX.Element {
  // ── Core state ───────────────────────────────────────────────────────────
  const [activeSector, setActiveSector] = useState<string>('Technology');
  const [searchQuery, setSearchQuery] = useState<string>('');
  const [now, setNow] = useState<Date>(new Date());
  const [signalCount, setSignalCount] = useState<number>(0);

  // ── DEV_MODE: mutable dummy data state ──────────────────────────────────
  const [devStocks, setDevStocks] = useState<StockObject[]>(
    () => structuredClone(DUMMY_STOCKS),
  );
  const [devHopeful, setDevHopeful] = useState<HopefulResponse>(
    () => structuredClone(DUMMY_HOPEFUL),
  );

  // ── Real hooks — only active when DEV_MODE is false ─────────────────────
  // React hooks must always be called (Rules of Hooks), but we pass an empty
  // sector string when in DEV_MODE so the hooks effectively no-op.
  const realStocks = useSectorFeed(DEV_MODE ? '' : activeSector);
  const realHopeful = useHopeful();

  // ── Derived data: pick dev or real ──────────────────────────────────────
  const stocks: StockObject[] = DEV_MODE
    ? (activeSector === 'Hopeful'
        ? [...devHopeful.leaders, ...devHopeful.sympathy]
        : devStocks.filter((s) => s.sector === activeSector))
    : (activeSector === 'Hopeful'
        ? [...realHopeful.leaders, ...realHopeful.sympathy]
        : realStocks);

  const hopefulLeaders = DEV_MODE ? devHopeful.leaders : realHopeful.leaders;
  const hopefulSympathy = DEV_MODE ? devHopeful.sympathy : realHopeful.sympathy;

  // ── Tab transition animation (JS inline styles, NOT CSS classes) ────────
  // useRef holds the feed container div so we can control opacity/translateY.
  const feedRef = useRef<HTMLDivElement>(null);
  const [feedStyle, setFeedStyle] = useState<CSSProperties>({
    opacity: 1,
    transform: 'translateY(0px)',
    transition: 'opacity 180ms ease, transform 180ms ease',
  });

  // onSectorChange: fade out → swap sector → fade in.
  // useCallback so SectorTabs doesn't re-render unnecessarily.
  const handleSectorChange = useCallback((sector: string) => {
    // Step 1: fade out (100ms).
    setFeedStyle({
      opacity: 0,
      transform: 'translateY(8px)',
      transition: 'opacity 100ms ease, transform 100ms ease',
    });

    // Step 2: after 100ms, swap sector + clear search, then fade in (180ms).
    setTimeout(() => {
      setActiveSector(sector);
      setSearchQuery('');
      setFeedStyle({
        opacity: 1,
        transform: 'translateY(0px)',
        transition: 'opacity 180ms ease, transform 180ms ease',
      });
    }, 100);
  }, []);

  // ── Clock: update "Updated HH:MM:SS" every second ──────────────────────
  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), 1000);
    return () => clearInterval(id);
  }, []);

  // ── Signal count: track cumulative new entries ──────────────────────────
  // Compare previous feed length to current — each new entry increments count.
  const prevLenRef = useRef<number>(stocks.length);
  useEffect(() => {
    const diff = stocks.length - prevLenRef.current;
    if (diff > 0) {
      setSignalCount((c) => c + diff);
    }
    prevLenRef.current = stocks.length;
  }, [stocks.length]);

  // ── DEV_MODE: simulate price jitter every 1200ms ───────────────────────
  // Applies small random price changes (±0.5%) to trigger flash animations.
  useEffect(() => {
    if (!DEV_MODE) return;

    const id = setInterval(() => {
      setDevStocks((prev) =>
        prev.map((s) => ({
          ...s,
          price: +(s.price * (1 + (Math.random() - 0.5) * 0.01)).toFixed(4),
        })),
      );
      setDevHopeful((prev) => ({
        leaders: prev.leaders.map((s) => ({
          ...s,
          price: +(s.price * (1 + (Math.random() - 0.5) * 0.01)).toFixed(4),
        })),
        sympathy: prev.sympathy.map((s) => ({
          ...s,
          price: +(s.price * (1 + (Math.random() - 0.5) * 0.01)).toFixed(4),
        })),
      }));
    }, 1200);

    return () => clearInterval(id);
  }, []);

  // ── DEV_MODE: simulate AAPL "generating" → "ready" after 4 seconds ─────
  useEffect(() => {
    if (!DEV_MODE) return;

    const timer = setTimeout(() => {
      setDevStocks((prev) =>
        prev.map((s) =>
          s.ticker === 'AAPL'
            ? {
                ...s,
                reason: 'Services revenue hit record high, offsetting iPhone softness.',
                reasonStatus: 'ready' as const,
              }
            : s,
        ),
      );
    }, 4000);

    return () => clearTimeout(timer);
  }, []);

  // ── Stat cards data ─────────────────────────────────────────────────────
  const hopefulActive = DEV_MODE
    ? devStocks.filter((s) => s.isHopeful).length
    : stocks.filter((s) => s.isHopeful).length;

  // Top mover: highest |changePercent| across all current data.
  const allStocks = DEV_MODE ? devStocks : stocks;
  const topMover = allStocks.length > 0
    ? allStocks.reduce((best, s) =>
        Math.abs(s.changePercent) > Math.abs(best.changePercent) ? s : best,
      )
    : null;

  return (
    <div className="min-h-screen bg-bg">
      {/* ── Header ─────────────────────────────────────────────────────── */}
      <header className="flex items-center justify-between px-4 py-3 border-b border-border bg-bg">
        <div>
          <h1 className="text-lg font-bold text-primary tracking-tight leading-none">
            Momentum
          </h1>
          <p className="text-xs text-muted mt-0.5">market intelligence</p>
        </div>
        <div className="flex items-center gap-3">
          {/* Updated timestamp — ticks every second */}
          <span className="text-xs text-hint">
            Updated {formatTime(now)}
          </span>
          {/* Live pill with pulsing green dot */}
          <span className="inline-flex items-center gap-1.5 text-xs text-secondary bg-surface px-2 py-1 rounded-full border border-border">
            <span
              className="inline-block rounded-full animate-pulse"
              style={{ width: '6px', height: '6px', background: '#1A6B3C' }}
            />
            Live · {allStocks.length} symbols
          </span>
        </div>
      </header>

      {/* ── Search bar ─────────────────────────────────────────────────── */}
      <div className="px-4 pt-3 pb-2">
        <div className="relative">
          <div className="absolute left-3 top-1/2 -translate-y-1/2">
            <SearchIcon />
          </div>
          <input
            type="text"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder="Search ticker — AAPL, AMTX..."
            className="w-full pl-8 pr-8 py-2 text-sm bg-surface border border-border rounded-lg text-primary placeholder:text-hint focus:outline-none focus:border-muted"
          />
          {/* Clear button — visible only when input has value */}
          {searchQuery && (
            <button
              onClick={() => setSearchQuery('')}
              className="absolute right-3 top-1/2 -translate-y-1/2 text-muted hover:text-secondary text-sm leading-none"
            >
              ✕
            </button>
          )}
        </div>
      </div>

      {/* ── Stat cards row (3 cards) ───────────────────────────────────── */}
      <div className="grid grid-cols-3 gap-3 px-4 pb-3">
        {/* Signals today */}
        <div className="bg-surface border border-border rounded-lg px-3 py-2">
          <p className="text-[10px] uppercase tracking-wide text-muted">Signals today</p>
          <p className="text-lg font-bold text-primary leading-tight mt-0.5">{signalCount}</p>
        </div>

        {/* Hopeful active */}
        <div className="bg-surface border border-border rounded-lg px-3 py-2">
          <p className="text-[10px] uppercase tracking-wide text-muted">Hopeful active</p>
          <p className="text-lg font-bold text-primary leading-tight mt-0.5">{hopefulActive}</p>
        </div>

        {/* Top mover */}
        <div className="bg-surface border border-border rounded-lg px-3 py-2">
          <p className="text-[10px] uppercase tracking-wide text-muted">Top mover</p>
          {topMover ? (
            <p className="text-lg font-bold leading-tight mt-0.5" style={{
              color: topMover.changePercent >= 0 ? '#1A6B3C' : '#8B1A1A',
            }}>
              {topMover.ticker}{' '}
              <span style={{ fontFamily: 'monospace' }}>
                {topMover.changePercent > 0 ? '+' : ''}
                {topMover.changePercent.toFixed(2)}%
              </span>
            </p>
          ) : (
            <p className="text-lg font-bold text-hint leading-tight mt-0.5">—</p>
          )}
        </div>
      </div>

      {/* ── Sector tab bar ─────────────────────────────────────────────── */}
      <SectorTabs
        activeSector={activeSector}
        onSectorChange={handleSectorChange}
      />

      {/* ── Feed area with tab transition animation ────────────────────── */}
      {/* feedStyle is controlled via JS state — opacity + translateY inline
          styles provide a fade-out (100ms) / fade-in (180ms) on tab switch. */}
      <main className="max-w-4xl mx-auto" ref={feedRef} style={feedStyle}>
        {activeSector === 'Hopeful' ? (
          <HopefulFeed
            leaders={hopefulLeaders}
            sympathy={hopefulSympathy}
            searchQuery={searchQuery}
            devMode={DEV_MODE}
          />
        ) : (
          <LiveFeed
            stocks={stocks}
            searchQuery={searchQuery}
            devMode={DEV_MODE}
          />
        )}
      </main>
    </div>
  );
}
