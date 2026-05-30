<script lang="ts">
  // Help / About modal. Triggered from the "?" button in the Topbar
  // (also opens on `?` key when no input is focused). Lists keyboard
  // shortcuts, the badge / colour semantics, and the build version.
  //
  // Build-info comes from /metrics' weft_webui_build_info gauge ;
  // we'd normally inject it at build time, but pulling it from the
  // already-running metrics endpoint keeps the SPA static.
  import { onMount, onDestroy } from 'svelte';

  let { open = $bindable(false) }: { open: boolean } = $props();
  let dialog: HTMLDialogElement;

  $effect(() => {
    if (open) dialog?.showModal();
    else dialog?.close();
  });

  // `?` opens when no input/textarea is focused (otherwise it'd
  // intercept the literal question-mark a user is typing).
  function onKey(e: KeyboardEvent) {
    if (e.key !== '?') return;
    const t = e.target as HTMLElement;
    if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return;
    e.preventDefault();
    open = !open;
  }
  onMount(() => window.addEventListener('keydown', onKey));
  onDestroy(() => window.removeEventListener('keydown', onKey));
</script>

<dialog class="modal" bind:this={dialog} onclose={() => (open = false)}>
  <div class="modal-box max-w-2xl">
    <h3 class="text-lg font-bold">Weft dashboard — quick reference</h3>
    <p class="mt-1 text-sm text-base-content/60">
      Conventions and shortcuts. Reload the page to pick up a new build.
    </p>

    <div class="mt-4 grid gap-6 sm:grid-cols-2">
      <section>
        <h4 class="text-xs font-semibold uppercase tracking-wide text-base-content/60">Keyboard</h4>
        <dl class="mt-2 space-y-1 text-sm">
          <div class="flex items-baseline gap-3">
            <kbd class="kbd kbd-xs">⌘K</kbd>
            <span class="text-base-content/40 text-xs">/</span>
            <kbd class="kbd kbd-xs">Ctrl-K</kbd>
            <span class="ml-auto text-base-content/70">Open search palette</span>
          </div>
          <div class="flex items-baseline gap-3">
            <kbd class="kbd kbd-xs">?</kbd>
            <span class="ml-auto text-base-content/70">Open this panel</span>
          </div>
          <div class="flex items-baseline gap-3">
            <kbd class="kbd kbd-xs">↑</kbd> <kbd class="kbd kbd-xs">↓</kbd>
            <span class="ml-auto text-base-content/70">Navigate palette results</span>
          </div>
          <div class="flex items-baseline gap-3">
            <kbd class="kbd kbd-xs">↵</kbd>
            <span class="ml-auto text-base-content/70">Open highlighted result</span>
          </div>
          <div class="flex items-baseline gap-3">
            <kbd class="kbd kbd-xs">Esc</kbd>
            <span class="ml-auto text-base-content/70">Close modal / drawer</span>
          </div>
        </dl>
      </section>

      <section>
        <h4 class="text-xs font-semibold uppercase tracking-wide text-base-content/60">Role badges</h4>
        <dl class="mt-2 space-y-1.5 text-sm">
          <div class="flex items-baseline gap-2">
            <span class="badge badge-error badge-sm uppercase tracking-wide">superadmin</span>
            <span class="ml-auto text-xs text-base-content/70">Cluster-wide UI (admin port)</span>
          </div>
          <div class="flex items-baseline gap-2">
            <span class="badge badge-warning badge-sm uppercase tracking-wide">admin</span>
            <span class="ml-auto text-xs text-base-content/70">Tenant administrator (user port)</span>
          </div>
          <div class="flex items-baseline gap-2">
            <span class="badge badge-info badge-sm">dev</span>
            <span class="ml-auto text-xs text-base-content/70">WEBUI_DEV_MODE=true</span>
          </div>
        </dl>
      </section>

      <section>
        <h4 class="text-xs font-semibold uppercase tracking-wide text-base-content/60">Live event toasts</h4>
        <dl class="mt-2 space-y-1.5 text-sm">
          <div class="flex items-baseline gap-2">
            <span class="inline-block h-3 w-3 rounded border-l-4 border-success"></span>
            <span class="font-mono text-xs">vm.state.*</span>
            <span class="ml-auto text-xs text-base-content/70">VM lifecycle</span>
          </div>
          <div class="flex items-baseline gap-2">
            <span class="inline-block h-3 w-3 rounded border-l-4 border-info"></span>
            <span class="font-mono text-xs">lb.*  ·  dns.*</span>
            <span class="ml-auto text-xs text-base-content/70">Network reconcile</span>
          </div>
          <div class="flex items-baseline gap-2">
            <span class="inline-block h-3 w-3 rounded border-l-4 border-primary"></span>
            <span class="font-mono text-xs">scheduling-rule.*</span>
            <span class="ml-auto text-xs text-base-content/70">Placement compliance</span>
          </div>
          <div class="flex items-baseline gap-2">
            <span class="inline-block h-3 w-3 rounded border-l-4 border-warning"></span>
            <span class="font-mono text-xs">security-group.*  ·  fip.*</span>
            <span class="ml-auto text-xs text-base-content/70">Security / public ingress</span>
          </div>
          <div class="flex items-baseline gap-2">
            <span class="inline-block h-3 w-3 rounded border-l-4 border-error"></span>
            <span class="font-mono text-xs">*.error  ·  *.failed</span>
            <span class="ml-auto text-xs text-base-content/70">Anything that didn't reconcile</span>
          </div>
        </dl>
      </section>

      <section>
        <h4 class="text-xs font-semibold uppercase tracking-wide text-base-content/60">Status badges</h4>
        <dl class="mt-2 space-y-1 text-sm">
          <div class="flex items-baseline gap-2">
            <span class="badge badge-sm badge-success">running</span>
            <span class="badge badge-sm badge-success">active</span>
            <span class="ml-auto text-xs text-base-content/70">Healthy / reachable</span>
          </div>
          <div class="flex items-baseline gap-2">
            <span class="badge badge-sm badge-info">available</span>
            <span class="ml-auto text-xs text-base-content/70">Idle, ready to attach</span>
          </div>
          <div class="flex items-baseline gap-2">
            <span class="badge badge-sm badge-warning">stopped</span>
            <span class="badge badge-sm badge-warning">draining</span>
            <span class="badge badge-sm badge-warning">provisioning</span>
            <span class="ml-auto text-xs text-base-content/70">Transitional / paused</span>
          </div>
          <div class="flex items-baseline gap-2">
            <span class="badge badge-sm badge-error">error</span>
            <span class="badge badge-sm badge-error">failed</span>
            <span class="ml-auto text-xs text-base-content/70">Needs operator attention</span>
          </div>
        </dl>
      </section>
    </div>

    <hr class="my-6 border-base-300" />

    <section class="text-xs text-base-content/60">
      <p>
        <strong class="text-base-content/80">weft-webui</strong> — dashboard for the
        <a href="https://github.com/openweft" class="link link-primary">openweft</a> platform.
        Talks to <code>weft agent</code> over gRPC (see <code>--weft-socket</code>) and to the
        sibling <code>weft-network</code> controller for routers / LBs / DNS / scheduling rules.
        Live events are bridged from <code>WatchEvents</code> to a Server-Sent Events
        stream consumed by every reactive surface in this UI.
      </p>
      <p class="mt-2">
        The exact build version lives at <code>/metrics</code> on the admin port
        (look for <code>weft_webui_build_info</code>) — operator territory.
      </p>
    </section>

    <div class="modal-action">
      <button class="btn btn-sm btn-primary" onclick={() => (open = false)}>Close</button>
    </div>
  </div>
  <form method="dialog" class="modal-backdrop"><button>close</button></form>
</dialog>
