// See ARCHITECTURE.md §10.2 — polls /stocks/{ticker}/reason every 2s
// Stops polling once status === 'ready'. Shows 'Analyzing...' until then.
// Rule 4 (WINDSURF.md): polling only, no WebSocket on the frontend
// See ARCHITECTURE.md §6.2 — async reason latency contract

import { useEffect, useRef, useState } from 'react';
import type { ReasonResponse } from '../types';

interface UseReasonResult {
  reason: string;
  status: 'ready' | 'generating' | 'unavailable';
}

/**
 * Polls GET /stocks/{ticker}/reason every 2 seconds.
 * Once status === 'ready', clears the interval and stops polling.
 * Returns { reason, status } for display in StockRow.
 */
export function useReason(ticker: string): UseReasonResult {
  const [reason, setReason] = useState<string>('');
  const [status, setStatus] = useState<UseReasonResult['status']>('generating');
  const intervalRef = useRef<number | null>(null);

  useEffect(() => {
    // Reset state on ticker change.
    setReason('');
    setStatus('generating');

    const controller = new AbortController();

    const poll = async (): Promise<void> => {
      try {
        const res = await fetch(`/stocks/${ticker}/reason`, {
          signal: controller.signal,
        });
        if (res.ok) {
          const data: ReasonResponse = await res.json();
          setReason(data.reason);
          setStatus(data.status);

          // Stop polling once the reason is ready or unavailable.
          if (data.status === 'ready' || data.status === 'unavailable') {
            if (intervalRef.current !== null) {
              clearInterval(intervalRef.current);
              intervalRef.current = null;
            }
          }
        }
      } catch {
        // AbortError on cleanup or network failure — silently ignore.
      }
    };

    poll();
    intervalRef.current = window.setInterval(poll, 2000);

    return () => {
      controller.abort();
      if (intervalRef.current !== null) {
        clearInterval(intervalRef.current);
        intervalRef.current = null;
      }
    };
  }, [ticker]);

  return { reason, status };
}
