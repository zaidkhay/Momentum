// See ARCHITECTURE.md §10.2 — polls /sectors/{activeSector} every 1s
// Rule 4 (WINDSURF.md): polling only, no WebSocket on the frontend

import { useEffect, useState } from 'react';
import type { StockObject } from '../types';

/**
 * Polls GET /sectors/{sector} every 1 second.
 * Returns the latest stock array for the active sector.
 * Resets stocks to [] on sector change to avoid stale flash.
 */
export function useSectorFeed(sector: string): StockObject[] {
  const [stocks, setStocks] = useState<StockObject[]>([]);

  useEffect(() => {
    // Reset on sector change so the UI doesn't flash stale data.
    setStocks([]);

    const controller = new AbortController();

    const poll = async (): Promise<void> => {
      try {
        const res = await fetch(`/sectors/${sector}`, {
          signal: controller.signal,
        });
        if (res.ok) {
          const data: StockObject[] = await res.json();
          setStocks(data);
        }
      } catch {
        // AbortError on cleanup or network failure — silently ignore.
      }
    };

    // Fire immediately, then every 1 second.
    poll();
    const id = setInterval(poll, 1000);

    return () => {
      controller.abort();
      clearInterval(id);
    };
  }, [sector]);

  return stocks;
}
