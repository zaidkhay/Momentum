// See ARCHITECTURE.md §10.2 — polls /stocks/{ticker}/reason every 2s
// Stops polling once status === 'ready'. Shows 'Analyzing...' until then.
// Rule 4 (WINDSURF.md): polling only, no WebSocket on the frontend
// TODO: See ARCHITECTURE.md §10.2 — setInterval 2000ms → fetch → if ready, clearInterval

export function useReason(): void {
  // TODO: See ARCHITECTURE.md §10.2 + §6.2 — async reason latency contract
}
