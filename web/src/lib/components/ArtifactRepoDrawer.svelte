<script lang="ts">
  // Right-side drawer that lists every tag of one repository inside
  // one registry. Opened from RegistryPage's grouped artifacts table
  // — the table shows one row per (registry, repository) ; this
  // drawer surfaces the full tag list.
  //
  // Tags are plain rows here (read-only for now) ; once tag deletion
  // / garbage-collection wires up, this is the place to add per-tag
  // action buttons.
  import type { Row } from '../api';

  let {
    registry,
    repository,
    artifacts,
    onClose,
  }: {
    registry: string;
    repository: string;
    artifacts: Row[];          // full set of (repo, tag, …) rows for this repo
    onClose: () => void;
  } = $props();

  // Sort tags : "latest" first, then semver-ish descending. The
  // grouping is done client-side anyway, so the cost is negligible.
  let sortedTags = $derived.by<Row[]>(() => {
    const cp = [...artifacts];
    cp.sort((a, b) => {
      if (a.tag === 'latest') return -1;
      if (b.tag === 'latest') return 1;
      return String(b.tag).localeCompare(String(a.tag), undefined, { numeric: true });
    });
    return cp;
  });

  let head = $derived(artifacts[0] ?? null);
  let kind = $derived(String(head?.type ?? '—'));
  let arches = $derived(String(head?.arch ?? '—'));

  function typeBadge(t: unknown): string {
    switch (String(t).toLowerCase()) {
      case 'container': return 'badge-info';
      case 'raw':       return 'badge-warning';
      case 'chart':     return 'badge-success';
      case 'model':     return 'badge-secondary';
      default:          return 'badge-ghost';
    }
  }
</script>

<button class="fixed inset-0 z-40 bg-base-300/40" aria-label="Close drawer" onclick={onClose}></button>

<aside class="fixed inset-y-0 right-0 z-50 flex w-full max-w-3xl flex-col bg-base-100 shadow-2xl">
  <header class="flex shrink-0 items-center gap-3 border-b border-base-300 px-5 py-3">
    <div class="min-w-0">
      <h2 class="text-lg font-bold font-mono truncate">{repository}</h2>
      <p class="text-xs text-base-content/60">
        in <span class="font-mono">{registry}</span>
        · <span class="badge badge-xs {typeBadge(kind)}">{kind}</span>
        · {artifacts.length} {artifacts.length === 1 ? 'tag' : 'tags'}
        · {arches}
      </p>
    </div>
    <button class="ml-auto btn btn-sm btn-ghost" aria-label="Close" onclick={onClose}>✕</button>
  </header>

  <div class="min-h-0 flex-1 overflow-y-auto p-5">
    <h3 class="text-sm font-semibold uppercase tracking-wide text-base-content/60 mb-2">Tags</h3>

    <div class="rounded-box border border-base-300 bg-base-100">
      <table class="table table-sm">
        <thead>
          <tr>
            <th>Tag</th>
            <th>Architectures</th>
            <th>Size</th>
            <th>Pushed</th>
          </tr>
        </thead>
        <tbody>
          {#each sortedTags as t (`${t.repository}:${t.tag}`)}
            <tr class="hover">
              <td class="font-mono">
                {t.tag}
                {#if t.tag === 'latest'}<span class="ml-1 badge badge-xs badge-success">latest</span>{/if}
              </td>
              <td class="text-xs">{t.arch}</td>
              <td class="font-mono text-xs">{t.size}</td>
              <td class="text-xs text-base-content/70">{t.pushed}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>

    <p class="mt-3 text-xs text-base-content/50">
      Read-only for now — per-tag mutation (delete, sign, replicate) lands once the registry GC + signing flows are wired into the dashboard.
    </p>
  </div>
</aside>
