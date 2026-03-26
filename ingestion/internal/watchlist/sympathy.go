// Package watchlist manages the dynamic set of tickers subscribed to the
// Alpaca WebSocket stream. This file holds only the architect-managed
// sympathy map — no logic lives here.
//
// ARCHITECT-MANAGED — never auto-generate or infer entries.
// Add new entries manually as new Hopeful stocks are discovered.
// Removing an entry stops sympathy-play subscriptions on next restart.
//
// See ARCHITECTURE.md §4.4 — sympathy play logic.
// See ARCHITECTURE.md §4.3 — Hopeful promotion criteria.
package watchlist

// SympathyMap maps each Hopeful leader ticker to its known sympathy peers.
// When a leader is promoted to Hopeful (see PromoteToHopeful in manager.go),
// any peer not already in the active watchlist is automatically subscribed.
//
// Exported (capital S) so the Hopeful promoter in Step 5 can read it directly
// without importing a separate package.
var SympathyMap = map[string][]string{
	// AMTX (Aemetis) — cellulosic ethanol / sustainable aviation fuel.
	// Peers move together on biofuel policy news and energy credit pricing.
	"AMTX": {"GEVO", "REGI", "VGFC"},

	// IMRX (Immunovant) — FcRn antibody platform for autoimmune disease.
	// Peers are in the same mechanism-of-action cluster; cross-react on
	// FDA decisions and clinical readouts in the autoimmune biotech space.
	"IMRX": {"ARQT", "PRTA", "HIMS"},

	// EONR — oil royalty micro-cap exposed to Permian/DJ Basin pricing.
	// Peers react to the same WTI spot moves and royalty deal flow.
	"EONR": {"VAALCO", "CIVI", "RING"},

	// MARA (Marathon Digital) — Bitcoin mining infrastructure.
	// Entire sector trades as a leveraged proxy for BTC spot price;
	// any MARA spike drags RIOT, CLSK, BTBT within minutes.
	"MARA": {"RIOT", "CLSK", "BTBT"},

	// SAVA (Cassava Sciences) — Alzheimer's disease (simufilam).
	// Peers are in neuro-degen biotech; co-move on AD trial news,
	// FDA advisory panel dates, and short-seller reports.
	"SAVA": {"ANAVEX", "PRTA", "AGEN"},
}
