// See ARCHITECTURE.md §10.1 — wire SectorTabs, LiveFeed, HopefulFeed together

import { useState } from 'react';
import SectorTabs from './components/SectorTabs';
import LiveFeed from './components/LiveFeed';
import HopefulFeed from './components/HopefulFeed';

export default function App(): JSX.Element {
  const [activeSector, setActiveSector] = useState<string>('Technology');

  return (
    <div className="min-h-screen bg-bg">
      {/* Header */}
      <header className="px-4 py-3 border-b border-border bg-bg">
        <h1 className="text-lg font-bold text-primary tracking-tight">
          Momentum
        </h1>
        <p className="text-xs text-muted mt-0.5">
          Real-time sector momentum dashboard
        </p>
      </header>

      {/* Sector tab bar */}
      <SectorTabs
        activeSector={activeSector}
        onSectorChange={setActiveSector}
      />

      {/* Feed area — Hopeful gets its own layout, others use LiveFeed */}
      <main className="max-w-4xl mx-auto">
        {activeSector === 'Hopeful' ? (
          <HopefulFeed />
        ) : (
          <LiveFeed sector={activeSector} />
        )}
      </main>
    </div>
  );
}
