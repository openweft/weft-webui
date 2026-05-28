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

// Per-quota-type line-icons (24×24, stroke = currentColor).
const QUOTA_ICONS: Record<string, string> = {
  cpu: `<rect x="6" y="6" width="12" height="12" rx="1.5" ${stroke}/><rect x="9.5" y="9.5" width="5" height="5" rx="1" ${stroke}/><path d="M9 3v3M15 3v3M9 18v3M15 18v3M3 9h3M3 15h3M18 9h3M18 15h3" ${stroke}/>`,
  ram: `<rect x="3" y="7" width="18" height="10" rx="1.5" ${stroke}/><path d="M7 17v2M11 17v2M15 17v2M7 10v4M11 10v4M15 10v4" ${stroke}/>`,
  microvm: `<rect x="4" y="5" width="16" height="6" rx="1.5" ${stroke}/><rect x="4" y="13" width="16" height="6" rx="1.5" ${stroke}/><path d="M7.5 8h.01M7.5 16h.01" ${stroke}/>`,
  vm: `<rect x="3" y="4" width="18" height="13" rx="1.5" ${stroke}/><path d="M8 21h8M12 17v4" ${stroke}/>`,
  volume: `<ellipse cx="12" cy="6" rx="7" ry="2.6" ${stroke}/><path d="M5 6v12c0 1.4 3.1 2.6 7 2.6s7-1.2 7-2.6V6" ${stroke}/>`,
  storage: ICONS.Storage,
  bucket: `<path d="M5 7h14l-1.4 12.2a1 1 0 0 1-1 .8H7.4a1 1 0 0 1-1-.8L5 7z" ${stroke}/><ellipse cx="12" cy="7" rx="7" ry="2.4" ${stroke}/>`,
  ip: `<path d="M4 8.5 12 4l8 4.5v7L12 20l-8-4.5z" ${stroke}/><path d="M9 11.5h2.2a1.6 1.6 0 0 1 0 3.2H9V11.5zm0 0v5" ${stroke}/>`,
  image: `<rect x="3" y="5" width="18" height="14" rx="2" ${stroke}/><circle cx="8.5" cy="10" r="1.5" ${stroke}/><path d="m4 17 5-4 4 3 3-2 4 3" ${stroke}/>`,
  // GPU : card + fan motif, distinct from cpu (chip lattice).
  gpu: `<rect x="3" y="7" width="18" height="10" rx="1.5" ${stroke}/><circle cx="8" cy="12" r="2.4" ${stroke}/><path d="M8 9.6v4.8M5.6 12h4.8" ${stroke}/><path d="M14 11h4M14 13h4" ${stroke}/>`,
};

export function quotaIcon(icon: string): string {
  return QUOTA_ICONS[icon] ?? QUOTA_ICONS.cpu;
}
