<script lang="ts">
  // SecurityPage — unified master-detail view of security groups and
  // their rules. Replaces ResourcePage routing for security-groups.
  //
  //   Master (left) : group list + N/E/D for the group itself.
  //   Detail (right) : rules of the selected group + N/E/D for one
  //     rule. setSecurityGroupRules is atomic so add / delete go
  //     through the whole-list PUT under the hood, but the operator
  //     sees per-row affordances.
  //
  // Aligned on DNSPage : same panes, same buttons, same selection UX.
  import {
    getRowsPage, getMe, deleteSecurityGroup,
    getSecurityGroupRules, setSecurityGroupRules,
    isEnabled,
    type ResourceMeta, type Row, type Me, type SecurityRule,
  } from '../api';
  import CreateSecurityGroupModal from './CreateSecurityGroupModal.svelte';
  import EditSecurityGroupModal from './EditSecurityGroupModal.svelte';
  import EditSecurityRuleModal from './EditSecurityRuleModal.svelte';

  let { meta }: { meta: ResourceMeta } = $props();

  let me = $state<Me | null>(null);
  let canEdit = $derived(!!me && (me.cluster_admin || me.tenant_admin));
  $effect(() => { getMe().then((u) => (me = u)).catch(() => {/* api.ts handled */}); });

  // ---- groups (master) ----
  let groups = $state<Row[]>([]);
  let groupsLoading = $state(true);
  let groupsErr = $state('');
  let groupQuery = $state('');
  let selectedGroupUUID = $state('');

  function groupUUID(g: Row): string { return String(g.uuid ?? g.name ?? ''); }

  function refreshGroups() {
    groupsLoading = true; groupsErr = '';
    getRowsPage('security-groups', { limit: 200 })
      .then((p) => {
        groups = p.rows;
        if (selectedGroupUUID && !p.rows.find((g) => groupUUID(g) === selectedGroupUUID)) {
          selectedGroupUUID = p.rows.length > 0 ? groupUUID(p.rows[0]) : '';
        } else if (!selectedGroupUUID && p.rows.length > 0) {
          selectedGroupUUID = groupUUID(p.rows[0]);
        }
      })
      .catch((e) => (groupsErr = String(e)))
      .finally(() => (groupsLoading = false));
  }
  $effect(refreshGroups);

  let filteredGroups = $derived.by(() => {
    const q = groupQuery.trim().toLowerCase();
    if (!q) return groups;
    return groups.filter((g) =>
      String(g.name).toLowerCase().includes(q)
      || String(g.description ?? '').toLowerCase().includes(q)
      || String(g.project ?? '').toLowerCase().includes(q),
    );
  });

  let selectedGroup = $derived<Row | null>(
    groups.find((g) => groupUUID(g) === selectedGroupUUID) ?? null,
  );

  function clickGroup(g: Row) {
    const id = groupUUID(g);
    selectedGroupUUID = selectedGroupUUID === id ? '' : id;
    groupActionErr = '';
  }

  let groupCreateOpen = $state(false);
  let groupActionBusy = $state(false);
  let groupActionErr = $state('');

  // Rename + description editor for the selected group. Rules are
  // edited from the right pane.
  let groupEditOpen = $state(false);

  async function delSelectedGroup() {
    if (!selectedGroup) return;
    if (!confirm(`Delete security group "${selectedGroup.name}" ?`)) return;
    groupActionBusy = true; groupActionErr = '';
    try {
      await deleteSecurityGroup(selectedGroupUUID);
      selectedGroupUUID = '';
      refreshGroups();
    } catch (e) {
      groupActionErr = String(e);
    } finally {
      groupActionBusy = false;
    }
  }

  // ---- rules (detail) ----
  let rules = $state<SecurityRule[]>([]);
  let rulesLoading = $state(false);
  let rulesErr = $state('');
  let ruleQuery = $state('');
  let selectedRuleIdx = $state<number>(-1);
  let ruleActionBusy = $state(false);
  let ruleActionErr = $state('');

  async function refreshRules() {
    if (!selectedGroupUUID) { rules = []; selectedRuleIdx = -1; return; }
    rulesLoading = true; rulesErr = '';
    try {
      rules = await getSecurityGroupRules(selectedGroupUUID);
      if (selectedRuleIdx >= rules.length) selectedRuleIdx = -1;
    } catch (e) {
      rulesErr = String(e);
    } finally {
      rulesLoading = false;
    }
  }
  $effect(() => { selectedGroupUUID; selectedRuleIdx = -1; refreshRules(); });

  let filteredRulesWithIdx = $derived.by(() => {
    const q = ruleQuery.trim().toLowerCase();
    const all = rules.map((r, i) => ({ rule: r, idx: i }));
    if (!q) return all;
    return all.filter(({ rule: r }) =>
      r.direction.toLowerCase().includes(q)
      || r.protocol.toLowerCase().includes(q)
      || String(r.port_min).includes(q)
      || String(r.port_max).includes(q)
      || (r.remote_cidr ?? '').toLowerCase().includes(q)
      || (r.remote_group_uuid ?? '').toLowerCase().includes(q),
    );
  });

  let selectedRule = $derived<SecurityRule | null>(
    selectedRuleIdx >= 0 && selectedRuleIdx < rules.length ? rules[selectedRuleIdx] : null,
  );

  function clickRule(idx: number) {
    selectedRuleIdx = selectedRuleIdx === idx ? -1 : idx;
    ruleActionErr = '';
  }

  // ---- rule edit modal ----
  let ruleEditOpen = $state(false);
  let ruleEditMode = $state<'create' | 'edit'>('create');

  function startRuleNew() {
    ruleEditMode = 'create';
    ruleEditOpen = true;
  }
  function startRuleEdit() {
    if (selectedRuleIdx < 0) return;
    ruleEditMode = 'edit';
    ruleEditOpen = true;
  }

  async function applyRuleSave(patched: SecurityRule) {
    if (!selectedGroupUUID) return;
    ruleActionBusy = true; ruleActionErr = '';
    try {
      let next: SecurityRule[];
      if (ruleEditMode === 'create') {
        next = [...rules, patched];
      } else if (selectedRuleIdx >= 0) {
        next = rules.map((r, i) => (i === selectedRuleIdx ? patched : r));
      } else {
        return;
      }
      await setSecurityGroupRules(selectedGroupUUID, next);
      rules = next;
      if (ruleEditMode === 'create') selectedRuleIdx = next.length - 1;
      ruleEditOpen = false;
    } catch (e) {
      ruleActionErr = String(e);
    } finally {
      ruleActionBusy = false;
    }
  }

  async function delSelectedRule() {
    if (selectedRuleIdx < 0) return;
    if (!selectedGroupUUID) return;
    const r = rules[selectedRuleIdx];
    if (!confirm(`Delete rule ${r.direction} ${r.protocol} ${r.port_min}-${r.port_max} ${r.remote_cidr || r.remote_group_uuid} ?`)) return;
    ruleActionBusy = true; ruleActionErr = '';
    try {
      const next = rules.filter((_, i) => i !== selectedRuleIdx);
      await setSecurityGroupRules(selectedGroupUUID, next);
      rules = next;
      selectedRuleIdx = -1;
      // Reflect rule count in the group list badge.
      refreshGroups();
    } catch (e) {
      ruleActionErr = String(e);
    } finally {
      ruleActionBusy = false;
    }
  }

  function directionBadge(d: string): string {
    return d === 'egress' ? 'badge-warning' : 'badge-info';
  }
  function fmtPorts(r: SecurityRule): string {
    if (r.port_min === 0 && r.port_max === 0) return 'any';
    if (r.port_min === r.port_max) return String(r.port_min);
    return `${r.port_min}–${r.port_max}`;
  }
  function fmtRemote(r: SecurityRule): string {
    return r.remote_cidr || r.remote_group_uuid || '—';
  }
</script>

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">{meta.label}</h2>
    <p class="text-sm text-base-content/60">
      Groups and rules · pick a group on the left to edit the rules attached to it on the right.
    </p>
  </div>
</div>

<div class="mt-4 flex gap-4">
  <!-- Groups master -->
  <section class="w-80 shrink-0 flex flex-col gap-2">
    <div class="flex items-center gap-2">
      <h3 class="text-sm font-semibold uppercase tracking-wide text-base-content/60">Groups</h3>
      {#if groupsLoading}<span class="loading loading-spinner loading-xs"></span>{/if}
      <span class="ml-auto text-xs text-base-content/50">{filteredGroups.length} of {groups.length}</span>
    </div>

    <label class="input input-sm input-bordered flex items-center gap-2">
      <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-50" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="11" cy="11" r="7" /><path d="m20 20-3-3" stroke-linecap="round" />
      </svg>
      <input type="search" class="grow" placeholder="Filter groups…" bind:value={groupQuery} />
    </label>

    {#if canEdit}
      <div class="flex flex-wrap gap-2">
        <button class="btn btn-sm btn-primary gap-1" onclick={() => (groupCreateOpen = true)}
          title="Create a new security group">
          <span class="text-base leading-none">+</span> New
        </button>
        <button class="btn btn-sm btn-warning gap-1"
          disabled={!selectedGroup || groupActionBusy}
          onclick={() => (groupEditOpen = true)}
          title={selectedGroup ? `Edit "${selectedGroup.name}"` : 'Select a group to edit'}>
          Edit
        </button>
        <button class="btn btn-sm btn-error gap-1"
          disabled={!selectedGroup || groupActionBusy}
          onclick={delSelectedGroup}
          title={selectedGroup ? `Delete "${selectedGroup.name}"` : 'Select a group to delete'}>
          {#if groupActionBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
          Delete
        </button>
      </div>
    {/if}

    {#if groupsErr}<div class="alert alert-error py-2 text-sm">{groupsErr}</div>{/if}
    {#if groupActionErr}<div class="alert alert-error py-2 text-sm">{groupActionErr}</div>{/if}

    <ul class="menu menu-sm w-full rounded-box border border-base-300 bg-base-100">
      {#each filteredGroups as g (groupUUID(g))}
        <li>
          <button class:menu-active={selectedGroupUUID === groupUUID(g)}
            onclick={() => clickGroup(g)}>
            <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-70" fill="none" stroke="currentColor" stroke-width="1.7">
              <path d="M12 3l8 4v5c0 5-4 8-8 9-4-1-8-4-8-9V7z" stroke-linejoin="round" />
            </svg>
            <div class="min-w-0 flex-1">
              <div class="flex items-baseline gap-2">
                <span class="truncate font-medium">{g.name}</span>
                {#if !isEnabled(g)}<span class="badge badge-xs badge-ghost">disabled</span>{/if}
              </div>
              <div class="text-[10px] text-base-content/50">
                {g.rules} rules · project {g.project ?? '—'}
              </div>
            </div>
          </button>
        </li>
      {:else}
        <li class="px-3 py-2 text-sm text-base-content/50">
          {groups.length === 0 ? 'No groups yet.' : 'No groups match the filter.'}
        </li>
      {/each}
    </ul>
  </section>

  <!-- Rules detail -->
  <section class="min-w-0 flex-1 flex flex-col gap-2">
    {#if !selectedGroup}
      <div class="rounded-box border border-base-300 bg-base-100 p-10 text-center text-base-content/50">
        Select a group to see its rules.
      </div>
    {:else}
      <div class="flex items-center gap-2">
        <div>
          <h3 class="text-sm font-semibold uppercase tracking-wide text-base-content/60">
            Rules in <span class="font-mono normal-case text-base-content">{selectedGroup.name}</span>
          </h3>
          <p class="text-xs text-base-content/50">
            {rules.length} rules · {selectedGroup.description || 'no description'}
          </p>
        </div>
        <div class="ml-auto flex items-center gap-2">
          <label class="input input-sm input-bordered flex items-center gap-2">
            <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-50" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="11" cy="11" r="7" /><path d="m20 20-3-3" stroke-linecap="round" />
            </svg>
            <input type="search" class="grow" placeholder="Filter rules…" bind:value={ruleQuery} />
          </label>
          {#if canEdit}
            <button class="btn btn-sm btn-primary gap-1" onclick={startRuleNew}
              title="Add a rule to {selectedGroup.name}">
              <span class="text-base leading-none">+</span> New
            </button>
            <button class="btn btn-sm btn-warning gap-1"
              disabled={!selectedRule || ruleActionBusy}
              onclick={startRuleEdit}
              title={selectedRule ? `Edit selected rule` : 'Select a rule to edit'}>
              Edit
            </button>
            <button class="btn btn-sm btn-error gap-1"
              disabled={!selectedRule || ruleActionBusy}
              onclick={delSelectedRule}
              title={selectedRule ? `Delete selected rule` : 'Select a rule to delete'}>
              {#if ruleActionBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
              Delete
            </button>
          {/if}
        </div>
      </div>

      {#if rulesErr}<div class="alert alert-error py-2 text-sm">{rulesErr}</div>{/if}
      {#if ruleActionErr}<div class="alert alert-error py-2 text-sm">{ruleActionErr}</div>{/if}

      <div class="rounded-box border border-base-300 bg-base-100">
        <table class="table table-sm">
          <thead>
            <tr>
              <th>Direction</th>
              <th>Protocol</th>
              <th>Ports</th>
              <th>Remote</th>
              <th>Enabled</th>
            </tr>
          </thead>
          <tbody>
            {#if rulesLoading}
              <tr><td colspan="5" class="py-8 text-center">
                <span class="loading loading-spinner"></span>
              </td></tr>
            {:else if filteredRulesWithIdx.length === 0}
              <tr><td colspan="5" class="py-8 text-center text-base-content/50">
                {rules.length === 0
                  ? 'No rules in this group. Add one with "+ New".'
                  : 'No rules match the filter.'}
              </td></tr>
            {:else}
              {#each filteredRulesWithIdx as { rule: r, idx } (idx)}
                <tr class="hover cursor-pointer"
                  class:bg-primary={selectedRuleIdx === idx}
                  class:text-primary-content={selectedRuleIdx === idx}
                  class:opacity-50={r.enabled === false}
                  onclick={() => clickRule(idx)}>
                  <td><span class="badge badge-sm {directionBadge(r.direction)}">{r.direction}</span></td>
                  <td><span class="badge badge-sm badge-ghost">{r.protocol}</span></td>
                  <td class="font-mono text-xs">{fmtPorts(r)}</td>
                  <td class="font-mono text-xs">{fmtRemote(r)}</td>
                  <td>
                    {#if r.enabled === false}
                      <span class="badge badge-sm badge-ghost">off</span>
                    {:else}
                      <span class="badge badge-sm badge-success">on</span>
                    {/if}
                  </td>
                </tr>
              {/each}
            {/if}
          </tbody>
        </table>
      </div>
    {/if}
  </section>
</div>

<CreateSecurityGroupModal bind:open={groupCreateOpen} onCreated={refreshGroups} />
<EditSecurityGroupModal bind:open={groupEditOpen} group={selectedGroup} onSaved={refreshGroups} />

{#if selectedGroup}
  <EditSecurityRuleModal
    bind:open={ruleEditOpen}
    rule={ruleEditMode === 'edit' ? selectedRule : null}
    mode={ruleEditMode}
    onSave={applyRuleSave}
  />
{/if}
