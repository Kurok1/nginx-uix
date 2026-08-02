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
          {{ t('backups.title') }}
        </h2>
        <p>{{ t('backups.description') }}</p>
      </div>
      <span>{{ t('backups.visible', { count: backups.length }) }}</span>
    </header>

    <p v-if="loading && backups.length === 0">
      {{ t('backups.loading') }}
    </p>
    <p v-else-if="backups.length === 0">
      {{ t('backups.empty') }}
    </p>

    <div
      v-else
      class="backup-inventory__table"
      data-backup-table
    >
      <table>
        <caption>{{ t('backups.caption') }}</caption>
        <thead>
          <tr>
            <th scope="col">
              {{ t('backups.backupId') }}
            </th>
            <th scope="col">
              {{ t('backups.source') }}
            </th>
            <th scope="col">
              {{ t('backups.state') }}
            </th>
            <th scope="col">
              {{ t('backups.verified') }}
            </th>
            <th scope="col">
              {{ t('backups.size') }}
            </th>
            <th scope="col">
              {{ t('backups.protection') }}
            </th>
            <th scope="col">
              {{ t('backups.actions') }}
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
            <td>{{ backup.verified_at === undefined ? t('backups.notVerified') : formatTime(backup.verified_at) }}</td>
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
                  {{ t('backups.restore') }}
                </button>
                <button
                  v-if="canProtect(backup)"
                  type="button"
                  data-action="protect"
                  @click="$emit('protect', backup)"
                >
                  {{ t('backups.protect') }}
                </button>
                <button
                  v-if="canUnprotect(backup)"
                  type="button"
                  data-action="unprotect"
                  @click="$emit('unprotect', backup)"
                >
                  {{ t('backups.removeProtection') }}
                </button>
                <span v-if="!hasActions(backup)">{{ t('backups.evidenceOnly') }}</span>
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
            {{ t('backups.backup', { id: abbreviate(backup.id) }) }}
          </h3>
          <StatusBadge
            :tone="stateTone(backup)"
            :label="stateLabel(backup)"
          />
        </header>
        <dl>
          <div><dt>{{ t('backups.source') }}</dt><dd>{{ sourceLabel(backup) }}</dd></div>
          <div><dt>{{ t('backups.verified') }}</dt><dd>{{ backup.verified_at === undefined ? t('backups.notVerified') : formatTime(backup.verified_at) }}</dd></div>
          <div><dt>{{ t('backups.size') }}</dt><dd>{{ formatBytes(backup.total_bytes) }}</dd></div>
          <div><dt>{{ t('backups.protection') }}</dt><dd>{{ protectionLabel(backup) }}</dd></div>
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
            {{ t('backups.restore') }}
          </button>
          <button
            v-if="canProtect(backup)"
            type="button"
            @click="$emit('protect', backup)"
          >
            {{ t('backups.protect') }}
          </button>
          <button
            v-if="canUnprotect(backup)"
            type="button"
            @click="$emit('unprotect', backup)"
          >
            {{ t('backups.removeProtection') }}
          </button>
        </div>
        <p v-else>
          {{ t('backups.noMutation') }}
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
      {{ loading ? t('common.loading') : t('backups.loadMore') }}
    </button>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type { ConfigBackup } from '../api/types'
import StatusBadge, { type StatusTone } from './StatusBadge.vue'

const { d, locale, n, t } = useI18n()

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
    case 'complete': return backup.body_present ? t('backups.states.complete') : t('backups.states.missingBody')
    case 'invalid': return t('backups.states.invalid')
    case 'deleting': return t('backups.states.deleting')
    case 'deleted': return t('backups.states.deleted')
    default: return t('backups.states.creating')
  }
}

function protectionTone(backup: ConfigBackup): StatusTone {
  if (backup.state === 'deleted') return 'unknown'
  return backup.protected ? 'warning' : 'success'
}

function protectionLabel(backup: ConfigBackup): string {
  if (backup.state === 'deleted') return t('backups.states.deleted')
  if (backup.manually_protected) return t('backups.protections.manuallyProtected')
  if (backup.protected) return t('backups.protections.systemProtected')
  return t('backups.protections.unprotected')
}

function protectionReasons(backup: ConfigBackup): string {
  const labels: Record<string, string> = {
    manual_protection: t('backups.protections.manualProtection'),
    minimum_complete: t('backups.protections.minimumComplete'),
    attention_case: t('backups.protections.attentionCase'),
    active_restore: t('backups.protections.activeRestore'),
  }
  return backup.protections
    .map(({ code }) => labels[code] ?? code)
    .join(locale.value === 'zh-CN' ? '、' : ', ')
}

function sourceLabel(backup: ConfigBackup): string {
  const key = backup.origin_type === 'release' ? 'backups.releaseSource' : 'backups.restoreSource'
  return t(key, { id: abbreviate(backup.origin_id) })
}

function abbreviate(value: string): string {
  return value.length <= 12 ? value : `${value.slice(0, 8)}…${value.slice(-4)}`
}

function formatBytes(value: number): string {
  if (value < 1024) return `${n(value, 'decimal')} B`
  if (value < 1024 * 1024) return `${n(value / 1024, 'decimal')} KiB`
  return `${n(value / (1024 * 1024), 'decimal')} MiB`
}

function formatTime(value: string): string {
  return d(new Date(value), 'short')
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
