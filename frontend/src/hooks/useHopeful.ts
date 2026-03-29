// See ARCHITECTURE.md §10.2 — polls /sectors/hopeful every 1s
// Rule 4 (WINDSURF.md): polling only, no WebSocket on the frontend

import { useEffect, useState } from 'react';
import type { HopefulResponse, StockObject } from '../types';

interface UseHopefulResult {
  leaders: StockObject[];
  sympathy: StockObject[];
}

/**
 * Polls GET /sectors/hopeful every 1 second.
 * Returns { leaders, sympathy } split per ARCHITECTURE.md §10.3.
 */
export function useHopeful(): UseHopefulResult {
  const [leaders, setLeaders] = useState<StockObject[]>([]);
  const [sympathy, setSympathy] = useState<StockObject[]>([]);

  useEffect(() => {
    const controller = new AbortController();

    const poll = async (): Promise<void> => {
      try {
        const res = await fetch('/sectors/hopeful', {
          signal: controller.signal,
        });
        if (res.ok) {
          const data: HopefulResponse = await res.json();
          setLeaders(data.leaders);
          setSympathy(data.sympathy);
        }
      } catch {
        // AbortError on cleanup or network failure — silently ignore.
      }
    };

    poll();
    const id = setInterval(poll, 1000);

    return () => {
      controller.abort();
      clearInterval(id);
    };
  }, []);

  return { leaders, sympathy };
}
