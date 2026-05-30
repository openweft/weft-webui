// Component coverage for QuotaBars — a pure presentational component
// (no fetch / no store subscriptions), so it tests well with no
// mocking beyond what @testing-library/svelte gives us by default.

import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/svelte';
import QuotaBars from './QuotaBars.svelte';
import type { QuotaBars as Bars } from '../api';

const FULL_BARS: Bars = {
  vcpu:         { used:  4, cap:  8, free:  4 },
  ram_gib:      { used:  6, cap: 16, free: 10 },
  gpus:         { used:  0, cap:  0, free:  0 },
  volumes:      { used:  3, cap: 10, free:  7 },
  volumes_gib:  { used: 50, cap: 100, free: 50 },
  shares:       { used:  1, cap:  5, free:  4 },
  shares_gib:   { used: 12, cap: 50, free: 38 },
  buckets:      { used:  2, cap:  8, free:  6 },
  buckets_gib:  { used:  4, cap: 32, free: 28 },
  registry_gib: { used:  1, cap: 16, free: 15 },
  floating_ips: { used:  0, cap:  4, free:  4 },
  projects:     { used:  2, cap:  5, free:  3 },
};

describe('QuotaBars', () => {
  it('renders all 12 dimensions by default', () => {
    const { container } = render(QuotaBars, { props: { bars: FULL_BARS } });
    // Each dimension is one rounded-box card. Tailwind's rounded-box
    // selector beats a brittle whole-DOM snapshot.
    expect(container.querySelectorAll('.rounded-box').length).toBe(12);
  });

  it('omits dimensions listed in `omit`', () => {
    const { container } = render(QuotaBars, {
      props: { bars: FULL_BARS, omit: ['projects', 'gpus'] },
    });
    expect(container.querySelectorAll('.rounded-box').length).toBe(10);
  });

  it('shows used / cap values per row', () => {
    const { getByText } = render(QuotaBars, { props: { bars: FULL_BARS } });
    // "4 / 8" appears for vcpu, "6 GiB / 16 GiB" for ram_gib.
    expect(getByText(/4 \/ 8/)).toBeInTheDocument();
    expect(getByText(/6 GiB \/ 16 GiB/)).toBeInTheDocument();
  });

  it('overlays `extra` on top of used and surfaces "+ requested"', () => {
    const { getByText } = render(QuotaBars, {
      props: { bars: FULL_BARS, extra: { vcpu: 2 } },
    });
    // Total = used (4) + extra (2) = 6 ; cap = 8.
    expect(getByText(/6 \/ 8/)).toBeInTheDocument();
    // The +N hint is rendered.
    expect(getByText(/\+2/)).toBeInTheDocument();
  });

  it('switches bar color to error when used ≥ 90% of cap', () => {
    const overBars: Bars = { ...FULL_BARS, vcpu: { used: 8, cap: 8, free: 0 } };
    const { container } = render(QuotaBars, { props: { bars: overBars } });
    // At least one progress bar carries progress-error.
    const errBars = container.querySelectorAll('.progress-error');
    expect(errBars.length).toBeGreaterThanOrEqual(1);
  });

  it('handles a zero-cap dimension without dividing by zero', () => {
    const zeroBars: Bars = { ...FULL_BARS, gpus: { used: 0, cap: 0, free: 0 } };
    const { container } = render(QuotaBars, { props: { bars: zeroBars } });
    // Zero-cap renders the "ghost" bar variant.
    expect(container.querySelector('.progress-ghost')).not.toBeNull();
  });
});
