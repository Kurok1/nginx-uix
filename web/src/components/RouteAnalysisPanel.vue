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
          Static analysis — prediction only
        </p>
        <h2 id="route-analysis-title">
          Candidate explanation
        </h2>
      </div>
      <StatusBadge
        :tone="analysis === null ? 'unknown' : analysis.complete ? 'success' : 'warning'"
        :label="analysis === null ? 'Not analyzed' : analysis.complete ? 'Complete prediction' : 'Indeterminate'"
      />
    </header>

    <p
      v-if="loading && analysis === null"
      class="route-analysis__empty"
      aria-live="polite"
    >
      <span aria-hidden="true">◌</span> Analyzing server and location candidates…
    </p>
    <p
      v-else-if="analysis === null"
      class="route-analysis__empty"
    >
      Enter request semantics and choose “Analyze route”. No request reaches Nginx during static analysis.
    </p>

    <template v-else>
      <p
        v-if="!analysis.complete"
        class="route-analysis__warning"
        role="status"
      >
        <span aria-hidden="true">△</span> Static analysis is indeterminate. Candidate evidence is retained below; no winner is inferred.
      </p>
      <p
        v-if="analysis.runtime_redirect_possible"
        class="route-analysis__notice"
      >
        <span aria-hidden="true">◇</span> Runtime URI changes may select a different final location.
      </p>

      <dl class="route-analysis__summary">
        <div>
          <dt>Normalized URI</dt>
          <dd><code>{{ analysis.normalized_uri }}</code></dd>
        </div>
        <div>
          <dt>Predicted server</dt>
          <dd><code>{{ analysis.predicted_server_route_id ?? 'Indeterminate' }}</code></dd>
        </div>
        <div>
          <dt>Predicted location</dt>
          <dd><code>{{ analysis.predicted_location_route_id ?? 'Server context' }}</code></dd>
        </div>
        <div v-if="analysis.predicted_tls_server_route_id !== undefined">
          <dt>Predicted TLS server</dt>
          <dd><code>{{ analysis.predicted_tls_server_route_id }}</code></dd>
        </div>
      </dl>

      <section aria-labelledby="route-server-candidates-title">
        <h3 id="route-server-candidates-title">
          Server candidates
        </h3>
        <p v-if="analysis.servers.length === 0">
          No server candidate could be projected.
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
            <p>Names: {{ candidate.server_names.join(', ') }}</p>
            <p>
              Listener:
              {{ candidate.listeners.map(listenerLabel).join(', ') }}
            </p>
            <p class="route-analysis__source">
              {{ sourceLabel(candidate.source) }}
            </p>
          </li>
        </ol>
      </section>

      <section aria-labelledby="route-location-candidates-title">
        <h3 id="route-location-candidates-title">
          Location candidates
        </h3>
        <p v-if="analysis.locations.length === 0">
          The selected server context has no projected location candidate.
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
import type {
  RouteAnalysis,
  RouteCandidateDisposition,
  RouteCandidateReason,
  RouteListener,
  RouteMatcherType,
  RouteSource,
} from '../api/route_lab'
import StatusBadge, { type StatusTone } from './StatusBadge.vue'

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
  return disposition.charAt(0).toUpperCase() + disposition.slice(1)
}

function reasonLabel(reason: RouteCandidateReason): string {
  const labels: Record<RouteCandidateReason, string> = {
    listener_mismatch: 'Listener does not match the requested scheme or port',
    listener_unsupported: 'Listener syntax cannot be proven statically',
    listener_default: 'Default server for the selected listener',
    server_name_exact: 'Exact server name match',
    server_name_leading_wildcard: 'Leading wildcard server name match',
    server_name_trailing_wildcard: 'Trailing wildcard server name match',
    server_name_regex: 'First matching server-name regex',
    server_name_lower_priority: 'A higher-priority server name matched',
    server_name_indeterminate: 'Server-name regex result is indeterminate',
    location_exact: 'Exact location match',
    location_longest_prefix: 'Longest matching prefix',
    location_prefix_priority: 'Priority prefix suppresses regex evaluation',
    location_regex: 'First matching location regex',
    location_shorter_prefix: 'A longer prefix matched',
    location_prefix_no_match: 'Prefix does not match the normalized URI',
    location_regex_no_match: 'Regex did not match under supported semantics',
    location_earlier_regex_selected: 'An earlier regex matched first',
    location_named_not_initial: 'Named locations are not initial request candidates',
    location_parent_matched: 'Parent location context matched',
    location_parent_not_selected: 'Parent location context was not selected',
    location_regex_indeterminate: 'PCRE behavior cannot be proven statically',
    location_uri_normalization_indeterminate: 'Selected server URI normalization is indeterminate',
  }
  return labels[reason]
}

function matcherLabel(type: RouteMatcherType, matcher: string): string {
  const typeLabels: Record<RouteMatcherType, string> = {
    unknown: 'Unknown',
    exact: 'Exact',
    prefix: 'Prefix',
    prefix_priority: 'Priority prefix (^~)',
    regex: 'Regex',
    regex_insensitive: 'Case-insensitive regex',
    named: 'Named location',
  }
  return `${typeLabels[type]} ${matcher}`.trim()
}

function listenerLabel(listener: RouteListener): string {
  const address = listener.address === '' ? '*' : listener.address
  const flags = [
    listener.ssl ? 'TLS' : 'HTTP',
    listener.default_server ? 'default' : '',
    listener.supported ? '' : 'unsupported',
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
