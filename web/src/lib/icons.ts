// Tiny original line-icons (24×24, stroke = currentColor) keyed by section.
// Deliberately hand-drawn from primitives so we depend on no icon set.
const stroke =
  'fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"';

const ICONS: Record<string, string> = {
  Overview: `<rect x="3" y="3" width="7" height="7" rx="1.5" ${stroke}/><rect x="14" y="3" width="7" height="7" rx="1.5" ${stroke}/><rect x="3" y="14" width="7" height="7" rx="1.5" ${stroke}/><rect x="14" y="14" width="7" height="7" rx="1.5" ${stroke}/>`,
  Identity: `<circle cx="12" cy="8" r="3.5" ${stroke}/><path d="M5 20c0-3.5 3.1-5.5 7-5.5s7 2 7 5.5" ${stroke}/>`,
  Network: `<circle cx="6" cy="6" r="2.5" ${stroke}/><circle cx="18" cy="6" r="2.5" ${stroke}/><circle cx="12" cy="18" r="2.5" ${stroke}/><path d="M7.7 7.8 11 15.5M16.3 7.8 13 15.5M8.5 6h7" ${stroke}/>`,
  Storage: `<ellipse cx="12" cy="6" rx="7" ry="2.6" ${stroke}/><path d="M5 6v6c0 1.4 3.1 2.6 7 2.6s7-1.2 7-2.6V6M5 12v6c0 1.4 3.1 2.6 7 2.6s7-1.2 7-2.6v-6" ${stroke}/>`,
  Compute: `<rect x="4" y="5" width="16" height="6" rx="1.5" ${stroke}/><rect x="4" y="13" width="16" height="6" rx="1.5" ${stroke}/><path d="M7.5 8h.01M7.5 16h.01" ${stroke}/>`,
  Admin: `<path d="M4 7h10M18 7h2M4 17h2M10 17h10" ${stroke}/><circle cx="16" cy="7" r="2.2" ${stroke}/><circle cx="8" cy="17" r="2.2" ${stroke}/>`,
};

export function sectionIcon(section: string): string {
  return ICONS[section] ?? ICONS.Overview;
}
