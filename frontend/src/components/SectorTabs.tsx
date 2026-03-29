// See ARCHITECTURE.md §10.1 — tab bar for all 9 sectors + Hopeful
// See ARCHITECTURE.md §1 — nine sectors including Hopeful

interface SectorTabsProps {
  activeSector: string;
  onSectorChange: (sector: string) => void;
}

// Order matches ARCHITECTURE.md §1 — Hopeful is the standout tab at the end.
const SECTORS: string[] = [
  'Technology',
  'Healthcare',
  'Energy',
  'Financials',
  'Consumer',
  'Industrials',
  'Materials',
  'Communication',
  'Hopeful',
];

export default function SectorTabs({
  activeSector,
  onSectorChange,
}: SectorTabsProps): JSX.Element {
  return (
    <nav className="flex gap-1 px-4 py-2 border-b border-border bg-bg overflow-x-auto">
      {SECTORS.map((sector) => {
        const isActive = sector === activeSector;
        return (
          <button
            key={sector}
            onClick={() => onSectorChange(sector)}
            className={`
              px-3 py-1.5 rounded text-xs font-medium whitespace-nowrap
              transition-colors duration-150
              ${
                isActive
                  ? 'bg-primary text-bg'
                  : 'text-secondary hover:bg-surface'
              }
            `}
          >
            {sector}
          </button>
        );
      })}
    </nav>
  );
}
