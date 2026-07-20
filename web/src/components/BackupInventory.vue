<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.2.3
-->
<template>
  <section
    class="backup-inventory"
    aria-labelledby="backup-inventory-title"
    :aria-busy="loading"
  >
    <header>
      <div>
        <h2 id="backup-inventory-title">
          Immutable backups
        </h2>
        <p>Only verified, present recovery points can start a restore.</p>
      </div>
      <span>{{ backups.length }} visible</span>
    </header>

    <p v-if="loading && backups.length === 0">
      Loading backup evidence…
    </p>
    <p v-else-if="backups.length === 0">
      No indexed backup recovery points are available.
    </p>

    <div
      v-else
      class="backup-inventory__table"
      data-backup-table
    >
      <table>
        <caption>Indexed immutable recovery points and their current protection</caption>
        <thead>
          <tr>
            <th scope="col">
              Backup ID
            </th>
            <th scope="col">
              Source
            </th>
            <th scope="col">
              State
            </th>
            <th scope="col">
              Verified
            </th>
            <th scope="col">
              Size
            </th>
            <th scope="col">
              Protection
            </th>
            <th scope="col">
              Actions
            </th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="backup in backups"
            :key="backup.id"
          >
            <th scope="row">
              <code :title="backup.id">{{ abbreviate(backup.id) }}</code>
            </th>
            <td>{{ sourceLabel(backup) }}</td>
            <td>
              <StatusBadge
                :tone="stateTone(backup)"
                :label="stateLabel(backup)"
              />
            </td>
            <td>{{ backup.verified_at === undefined ? 'Not verified' : formatTime(backup.verified_at) }}</td>
            <td>{{ formatBytes(backup.total_bytes) }}</td>
            <td>
              <StatusBadge
                :tone="protectionTone(backup)"
                :label="protectionLabel(backup)"
              />
              <small v-if="backup.protections.length > 0">{{ protectionReasons(backup) }}</small>
            </td>
            <td>
              <div class="backup-inventory__actions">
                <button
                  v-if="canRestore(backup)"
                  type="button"
                  data-action="restore"
                  @click="$emit('restore', backup)"
                >
                  Restore
                </button>
                <button
                  v-if="canProtect(backup)"
                  type="button"
                  data-action="protect"
                  @click="$emit('protect', backup)"
                >
                  Protect
                </button>
                <button
                  v-if="canUnprotect(backup)"
                  type="button"
                  data-action="unprotect"
                  @click="$emit('unprotect', backup)"
                >
                  Remove manual protection
                </button>
                <span v-if="!hasActions(backup)">Evidence only</span>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div
      v-if="backups.length > 0"
      class="backup-inventory__cards"
      data-backup-cards
    >
      <article
        v-for="backup in backups"
        :key="backup.id"
        :aria-labelledby="`backup-card-${backup.id}`"
      >
        <header>
          <h3 :id="`backup-card-${backup.id}`">
            Backup <code>{{ abbreviate(backup.id) }}</code>
          </h3>
          <StatusBadge
            :tone="stateTone(backup)"
            :label="stateLabel(backup)"
          />
        </header>
        <dl>
          <div><dt>Source</dt><dd>{{ sourceLabel(backup) }}</dd></div>
          <div><dt>Verified</dt><dd>{{ backup.verified_at === undefined ? 'Not verified' : formatTime(backup.verified_at) }}</dd></div>
          <div><dt>Size</dt><dd>{{ formatBytes(backup.total_bytes) }}</dd></div>
          <div><dt>Protection</dt><dd>{{ protectionLabel(backup) }}</dd></div>
        </dl>
        <p v-if="backup.protections.length > 0">
          {{ protectionReasons(backup) }}
        </p>
        <div
          v-if="hasActions(backup)"
          class="backup-inventory__actions"
        >
          <button
            v-if="canRestore(backup)"
            type="button"
            @click="$emit('restore', backup)"
          >
            Restore
          </button>
          <button
            v-if="canProtect(backup)"
            type="button"
            @click="$emit('protect', backup)"
          >
            Protect
          </button>
          <button
            v-if="canUnprotect(backup)"
            type="button"
            @click="$emit('unprotect', backup)"
          >
            Remove manual protection
          </button>
        </div>
        <p v-else>
          Evidence only; this backup has no available mutation.
        </p>
      </article>
    </div>

    <button
      v-if="nextCursor !== ''"
      class="backup-inventory__load-more"
      type="button"
      :disabled="loading"
      @click="$emit('load-more')"
    >
      {{ loading ? 'Loading…' : 'Load more backups' }}
    </button>
  </section>
</template>

<script setup lang="ts">
import type { ConfigBackup } from '../api/types'
import StatusBadge, { type StatusTone } from './StatusBadge.vue'

defineProps<{
  backups: ConfigBackup[]
  loading: boolean
  nextCursor: string
}>()

defineEmits<{
  'load-more': []
  protect: [backup: ConfigBackup]
  restore: [backup: ConfigBackup]
  unprotect: [backup: ConfigBackup]
}>()

function canRestore(backup: ConfigBackup): boolean {
  return backup.state === 'complete' && backup.body_present && backup.verified_at !== undefined
}

function canProtect(backup: ConfigBackup): boolean {
  return backup.state === 'complete' && backup.body_present && !backup.manually_protected
}

function canUnprotect(backup: ConfigBackup): boolean {
  return backup.state !== 'deleted' && backup.manually_protected
}

function hasActions(backup: ConfigBackup): boolean {
  return canRestore(backup) || canProtect(backup) || canUnprotect(backup)
}

function stateTone(backup: ConfigBackup): StatusTone {
  switch (backup.state) {
    case 'complete': return 'success'
    case 'invalid':
    case 'deleting': return 'warning'
    case 'deleted': return 'unknown'
    default: return 'unknown'
  }
}

function stateLabel(backup: ConfigBackup): string {
  switch (backup.state) {
    case 'complete': return backup.body_present ? 'Complete' : 'Missing body'
    case 'invalid': return 'Invalid'
    case 'deleting': return 'Deleting'
    case 'deleted': return 'Deleted'
    default: return 'Creating'
  }
}

function protectionTone(backup: ConfigBackup): StatusTone {
  if (backup.state === 'deleted') return 'unknown'
  return backup.protected ? 'warning' : 'success'
}

function protectionLabel(backup: ConfigBackup): string {
  if (backup.state === 'deleted') return 'Deleted'
  if (backup.manually_protected) return 'Manually protected'
  if (backup.protected) return 'System protected'
  return 'Unprotected'
}

function protectionReasons(backup: ConfigBackup): string {
  return backup.protections.map(({ code }) => code.replaceAll('_', ' ')).join(', ')
}

function sourceLabel(backup: ConfigBackup): string {
  return `${backup.origin_type === 'release' ? 'Release' : 'Restore'} ${abbreviate(backup.origin_id)}`
}

function abbreviate(value: string): string {
  return value.length <= 12 ? value : `${value.slice(0, 8)}…${value.slice(-4)}`
}

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`
  return `${(value / (1024 * 1024)).toFixed(1)} MiB`
}

function formatTime(value: string): string {
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(value))
}
</script>

<style scoped>
.backup-inventory {
  display: grid;
  min-width: 0;
  gap: var(--spacing-md);
}

.backup-inventory > header,
.backup-inventory article > header {
  display: flex;
  min-width: 0;
  align-items: start;
  justify-content: space-between;
  gap: var(--spacing-sm);
}

.backup-inventory h2,
.backup-inventory h3,
.backup-inventory p,
.backup-inventory dl {
  margin: 0;
}

.backup-inventory > header p,
.backup-inventory > header span,
.backup-inventory small {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
}

.backup-inventory__table {
  min-width: 0;
  overflow-x: auto;
}

.backup-inventory table {
  width: 100%;
  min-width: var(--component-operations-table-min-width);
  border-collapse: collapse;
  background: var(--color-canvas);
}

.backup-inventory caption {
  padding: var(--spacing-sm);
  text-align: start;
  font-size: var(--font-size-caption);
}

.backup-inventory th,
.backup-inventory td {
  padding: var(--spacing-sm);
  border-block-end: 1px solid var(--color-hairline);
  text-align: start;
  vertical-align: top;
}

.backup-inventory thead th {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
}

.backup-inventory td small {
  display: block;
  max-width: 20ch;
  margin-block-start: var(--spacing-xxs);
  overflow-wrap: anywhere;
}

.backup-inventory code {
  overflow-wrap: anywhere;
}

.backup-inventory__cards {
  display: none;
}

.backup-inventory__cards article {
  display: grid;
  min-width: 0;
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas);
  gap: var(--spacing-sm);
}

.backup-inventory dl {
  display: grid;
  gap: var(--spacing-xs);
}

.backup-inventory dl div {
  display: grid;
  grid-template-columns: minmax(7rem, 0.4fr) minmax(0, 1fr);
  gap: var(--spacing-sm);
}

.backup-inventory dt {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
}

.backup-inventory dd {
  min-width: 0;
  margin: 0;
  overflow-wrap: anywhere;
}

.backup-inventory__actions {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  gap: var(--spacing-xs);
}

.backup-inventory button {
  min-height: var(--component-control-min-size);
  padding: var(--spacing-xs) var(--spacing-sm);
  border: 1px solid var(--color-primary);
  border-radius: var(--rounded-pill);
  background: var(--color-canvas);
  color: var(--color-primary);
  cursor: pointer;
}

.backup-inventory button:disabled {
  border-color: var(--color-hairline);
  color: var(--color-ink-muted-80);
  cursor: not-allowed;
}

.backup-inventory__load-more {
  justify-self: start;
}

@media (max-width: 734px) {
  .backup-inventory__table {
    display: none;
  }

  .backup-inventory__cards {
    display: grid;
    gap: var(--spacing-sm);
  }
}

@media (max-width: 480px) {
  .backup-inventory > header,
  .backup-inventory article > header {
    flex-direction: column;
  }

  .backup-inventory dl div {
    grid-template-columns: 1fr;
    gap: 0;
  }
}
</style>
