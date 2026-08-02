<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.4.0
-->
<template>
  <section
    class="route-analysis"
    aria-labelledby="route-analysis-title"
    :aria-busy="loading"
  >
    <header>
      <div>
        <p class="route-analysis__eyebrow">
          {{ t('routeLab.analysisEyebrow') }}
        </p>
        <h2 id="route-analysis-title">
          {{ t('routeLab.analysis.title') }}
        </h2>
      </div>
      <StatusBadge
        :tone="analysis === null ? 'unknown' : analysis.complete ? 'success' : 'warning'"
        :label="analysis === null ? t('routeLab.analysis.notAnalyzed') : analysis.complete ? t('routeLab.analysis.complete') : t('routeLab.analysis.indeterminate')"
      />
    </header>

    <p
      v-if="loading && analysis === null"
      class="route-analysis__empty"
      aria-live="polite"
    >
      <span aria-hidden="true">◌</span> {{ t('routeLab.analysis.analyzing') }}
    </p>
    <p
      v-else-if="analysis === null"
      class="route-analysis__empty"
    >
      {{ t('routeLab.analysis.empty') }}
    </p>

    <template v-else>
      <p
        v-if="!analysis.complete"
        class="route-analysis__warning"
        role="status"
      >
        <span aria-hidden="true">△</span> {{ t('routeLab.analysis.warning') }}
      </p>
      <p
        v-if="analysis.runtime_redirect_possible"
        class="route-analysis__notice"
      >
        <span aria-hidden="true">◇</span> {{ t('routeLab.analysis.redirect') }}
      </p>

      <dl class="route-analysis__summary">
        <div>
          <dt>{{ t('routeLab.analysis.normalizedUri') }}</dt>
          <dd><code>{{ analysis.normalized_uri }}</code></dd>
        </div>
        <div>
          <dt>{{ t('routeLab.analysis.predictedServer') }}</dt>
          <dd><code>{{ analysis.predicted_server_route_id ?? t('routeLab.analysis.indeterminate') }}</code></dd>
        </div>
        <div>
          <dt>{{ t('routeLab.analysis.predictedLocation') }}</dt>
          <dd><code>{{ analysis.predicted_location_route_id ?? t('routeLab.analysis.serverContext') }}</code></dd>
        </div>
        <div v-if="analysis.predicted_tls_server_route_id !== undefined">
          <dt>{{ t('routeLab.analysis.predictedTlsServer') }}</dt>
          <dd><code>{{ analysis.predicted_tls_server_route_id }}</code></dd>
        </div>
      </dl>

      <section aria-labelledby="route-server-candidates-title">
        <h3 id="route-server-candidates-title">
          {{ t('routeLab.analysis.serverCandidates') }}
        </h3>
        <p v-if="analysis.servers.length === 0">
          {{ t('routeLab.analysis.noServerCandidates') }}
        </p>
        <ol
          v-else
          class="route-analysis__candidates"
        >
          <li
            v-for="candidate in analysis.servers"
            :key="candidate.route_id"
            :data-disposition="candidate.disposition"
          >
            <div>
              <StatusBadge
                :tone="dispositionTone(candidate.disposition)"
                :label="dispositionLabel(candidate.disposition)"
              />
              <code>{{ candidate.route_id }}</code>
            </div>
            <p><strong>{{ reasonLabel(candidate.reason) }}</strong></p>
            <p>{{ t('routeLab.analysis.names', { names: candidate.server_names.join(', ') }) }}</p>
            <p>
              {{ t('routeLab.analysis.listener', { listeners: candidate.listeners.map(listenerLabel).join(', ') }) }}
            </p>
            <p class="route-analysis__source">
              {{ sourceLabel(candidate.source) }}
            </p>
          </li>
        </ol>
      </section>

      <section aria-labelledby="route-location-candidates-title">
        <h3 id="route-location-candidates-title">
          {{ t('routeLab.analysis.locationCandidates') }}
        </h3>
        <p v-if="analysis.locations.length === 0">
          {{ t('routeLab.analysis.noLocationCandidates') }}
        </p>
        <ol
          v-else
          class="route-analysis__candidates"
        >
          <li
            v-for="candidate in analysis.locations"
            :key="candidate.route_id"
            :data-disposition="candidate.disposition"
          >
            <div>
              <StatusBadge
                :tone="dispositionTone(candidate.disposition)"
                :label="dispositionLabel(candidate.disposition)"
              />
              <code>{{ candidate.route_id }}</code>
            </div>
            <p><strong>{{ matcherLabel(candidate.matcher_type, candidate.matcher) }}</strong></p>
            <p>{{ reasonLabel(candidate.reason) }}</p>
            <p class="route-analysis__source">
              {{ sourceLabel(candidate.source) }}
            </p>
          </li>
        </ol>
      </section>
    </template>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type {
  RouteAnalysis,
  RouteCandidateDisposition,
  RouteCandidateReason,
  RouteListener,
  RouteMatcherType,
  RouteSource,
} from '../api/route_lab'
import StatusBadge, { type StatusTone } from './StatusBadge.vue'

const { t } = useI18n()

withDefaults(defineProps<{
  analysis: RouteAnalysis | null
  loading?: boolean
}>(), {
  loading: false,
})

function dispositionTone(disposition: RouteCandidateDisposition): StatusTone {
  switch (disposition) {
    case 'selected': return 'success'
    case 'matched': return 'unknown'
    case 'indeterminate': return 'warning'
    case 'excluded': return 'error'
  }
}

function dispositionLabel(disposition: RouteCandidateDisposition): string {
  const labels: Record<RouteCandidateDisposition, string> = {
    selected: t('routeLab.analysis.dispositions.selected'),
    matched: t('routeLab.analysis.dispositions.matched'),
    indeterminate: t('routeLab.analysis.dispositions.indeterminate'),
    excluded: t('routeLab.analysis.dispositions.excluded'),
  }
  return labels[disposition]
}

function reasonLabel(reason: RouteCandidateReason): string {
  const labels: Record<RouteCandidateReason, string> = {
    listener_mismatch: t('routeLab.analysis.reasons.listenerMismatch'),
    listener_unsupported: t('routeLab.analysis.reasons.listenerUnsupported'),
    listener_default: t('routeLab.analysis.reasons.listenerDefault'),
    server_name_exact: t('routeLab.analysis.reasons.serverNameExact'),
    server_name_leading_wildcard: t('routeLab.analysis.reasons.serverNameLeadingWildcard'),
    server_name_trailing_wildcard: t('routeLab.analysis.reasons.serverNameTrailingWildcard'),
    server_name_regex: t('routeLab.analysis.reasons.serverNameRegex'),
    server_name_lower_priority: t('routeLab.analysis.reasons.serverNameLowerPriority'),
    server_name_indeterminate: t('routeLab.analysis.reasons.serverNameIndeterminate'),
    location_exact: t('routeLab.analysis.reasons.locationExact'),
    location_longest_prefix: t('routeLab.analysis.reasons.locationLongestPrefix'),
    location_prefix_priority: t('routeLab.analysis.reasons.locationPrefixPriority'),
    location_regex: t('routeLab.analysis.reasons.locationRegex'),
    location_shorter_prefix: t('routeLab.analysis.reasons.locationShorterPrefix'),
    location_prefix_no_match: t('routeLab.analysis.reasons.locationPrefixNoMatch'),
    location_regex_no_match: t('routeLab.analysis.reasons.locationRegexNoMatch'),
    location_earlier_regex_selected: t('routeLab.analysis.reasons.locationEarlierRegexSelected'),
    location_named_not_initial: t('routeLab.analysis.reasons.locationNamedNotInitial'),
    location_parent_matched: t('routeLab.analysis.reasons.locationParentMatched'),
    location_parent_not_selected: t('routeLab.analysis.reasons.locationParentNotSelected'),
    location_regex_indeterminate: t('routeLab.analysis.reasons.locationRegexIndeterminate'),
    location_uri_normalization_indeterminate: t('routeLab.analysis.reasons.locationUriNormalizationIndeterminate'),
  }
  return labels[reason]
}

function matcherLabel(type: RouteMatcherType, matcher: string): string {
  const typeLabels: Record<RouteMatcherType, string> = {
    unknown: t('routeLab.analysis.matcherTypes.unknown'),
    exact: t('routeLab.analysis.matcherTypes.exact'),
    prefix: t('routeLab.analysis.matcherTypes.prefix'),
    prefix_priority: t('routeLab.analysis.matcherTypes.prefixPriority'),
    regex: t('routeLab.analysis.matcherTypes.regex'),
    regex_insensitive: t('routeLab.analysis.matcherTypes.regexInsensitive'),
    named: t('routeLab.analysis.matcherTypes.named'),
  }
  return `${typeLabels[type]} ${matcher}`.trim()
}

function listenerLabel(listener: RouteListener): string {
  const address = listener.address === '' ? '*' : listener.address
  const flags = [
    listener.ssl ? 'TLS' : 'HTTP',
    listener.default_server ? t('routeLab.analysis.listenerDefault') : '',
    listener.supported ? '' : t('routeLab.analysis.listenerUnsupported'),
  ].filter(Boolean)
  return `${address}:${listener.port} (${flags.join(', ')})`
}

function sourceLabel(source: RouteSource): string {
  return `${source.path}:${source.start_line}:${source.start_column}`
}
</script>

<style scoped>
.route-analysis,
.route-analysis section {
  display: grid;
  min-width: 0;
  align-content: start;
  gap: var(--spacing-md);
}

.route-analysis {
  padding: var(--spacing-lg);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  background: var(--color-canvas);
}

.route-analysis header,
.route-analysis__candidates li > div {
  display: flex;
  min-width: 0;
  align-items: start;
  justify-content: space-between;
  gap: var(--spacing-sm);
}

.route-analysis h2,
.route-analysis h3,
.route-analysis p,
.route-analysis dl,
.route-analysis ol {
  margin: 0;
}

.route-analysis__eyebrow,
.route-analysis__source {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
}

.route-analysis__empty,
.route-analysis__warning,
.route-analysis__notice {
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
}

.route-analysis__warning {
  border-color: var(--color-state-warning-foreground);
  background: var(--color-state-warning);
  color: var(--color-state-warning-foreground);
}

.route-analysis__summary {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--spacing-sm);
}

.route-analysis__summary div {
  min-width: 0;
  padding: var(--spacing-sm);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
}

.route-analysis dt {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
}

.route-analysis dd {
  margin: var(--spacing-xxs) 0 0;
  overflow-wrap: anywhere;
}

.route-analysis__candidates {
  display: grid;
  max-height: var(--component-route-candidate-max-height);
  overflow: auto;
  padding: 0;
  gap: var(--spacing-sm);
  list-style: none;
}

.route-analysis__candidates li {
  display: grid;
  min-width: 0;
  padding: var(--spacing-sm);
  border-inline-start: 3px solid var(--color-hairline);
  background: var(--color-canvas-parchment);
  gap: var(--spacing-xxs);
}

.route-analysis__candidates li[data-disposition='selected'] {
  border-inline-start-color: var(--color-state-success-foreground);
}

.route-analysis__candidates li[data-disposition='indeterminate'] {
  border-inline-start-color: var(--color-state-warning-foreground);
}

.route-analysis__candidates code {
  overflow-wrap: anywhere;
}

@media (max-width: 480px) {
  .route-analysis__summary {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
