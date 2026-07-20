<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.2.2
-->
<template>
  <section
    class="publish-panel"
    aria-labelledby="publish-panel-title"
    :aria-busy="phase === 'checking'"
  >
    <header>
      <div>
        <h2 id="publish-panel-title">
          Publish configuration
        </h2>
        <p>Validate the complete candidate before production can be changed.</p>
      </div>
      <button
        type="button"
        data-action="check"
        :disabled="blockedReason !== '' || phase === 'checking' || phase === 'queuing' || phase === 'tracking'"
        @click="$emit('check')"
      >
        {{ phase === 'checking' ? 'Checking complete candidate…' : 'Check publication' }}
      </button>
    </header>

    <p
      v-if="blockedReason !== ''"
      class="publish-panel__block"
      role="status"
    >
      {{ blockedReason }}
    </p>
    <p
      v-if="error !== ''"
      class="publish-panel__error"
      role="alert"
    >
      {{ error }}
    </p>

    <div
      v-if="check?.state === 'invalid'"
      class="publish-panel__invalid"
      role="alert"
    >
      <h3>Candidate validation failed</h3>
      <p>Production configuration has not been changed.</p>
      <ul tabindex="0">
        <li
          v-for="diagnostic in check.details.diagnostics"
          :key="`${diagnostic.code}:${diagnostic.path}:${diagnostic.line}`"
        >
          <code>{{ diagnostic.code }}</code>
          <strong>{{ diagnosticLocation(diagnostic.path, diagnostic.line) }}</strong>
          <span>{{ diagnostic.summary }}</span>
        </li>
      </ul>
    </div>

    <div
      v-else-if="check?.state === 'valid'"
      class="publish-panel__valid"
    >
      <p class="publish-panel__unchanged">
        ✓ Production configuration has not been changed.
      </p>
      <dl>
        <div><dt>Production</dt><dd><code>{{ shortDigest(check.production_digest) }}</code></dd></div>
        <div><dt>Draft</dt><dd><code>{{ shortDigest(check.draft_digest) }}</code></dd></div>
        <div><dt>Candidate</dt><dd><code>{{ shortDigest(check.candidate_digest) }}</code></dd></div>
        <div><dt>Validator</dt><dd>{{ check.validator_build_id }}</dd></div>
        <div><dt>Checked</dt><dd><time :datetime="check.finished_at">{{ formatTime(check.finished_at) }}</time></dd></div>
        <div><dt>Expires</dt><dd><time :datetime="check.expires_at">{{ formatTime(check.expires_at) }}</time></dd></div>
      </dl>
      <p
        v-if="expired"
        class="publish-panel__error"
        role="alert"
      >
        This check has expired. Run the complete check again.
      </p>
      <button
        type="button"
        data-action="publish"
        :disabled="expired || phase === 'queuing' || phase === 'tracking'"
        @click="$emit('publish')"
      >
        {{ phase === 'queuing' ? 'Queuing release…' : 'Publish…' }}
      </button>
    </div>
  </section>
</template>

<script setup lang="ts">
import type { PublishCheck } from '../api/types'

defineProps<{
  blockedReason: string
  check: PublishCheck | null
  error: string
  expired: boolean
  phase: 'idle' | 'checking' | 'checked' | 'queuing' | 'tracking'
}>()

defineEmits<{
  check: []
  publish: []
}>()

function shortDigest(digest: string): string {
  return digest.slice(0, 12)
}

function diagnosticLocation(path: string, line: number): string {
  if (path === '') return line > 0 ? `line ${line}` : 'candidate'
  return line > 0 ? `${path}:${line}` : path
}

function formatTime(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'medium' }).format(
    new Date(value),
  )
}
</script>

<style scoped>
.publish-panel {
  display: grid;
  min-width: 0;
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas);
  gap: var(--spacing-md);
}

.publish-panel header,
.publish-panel__valid,
.publish-panel__invalid,
.publish-panel__invalid li {
  display: grid;
  min-width: 0;
  gap: var(--spacing-xs);
}

.publish-panel header {
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
}

.publish-panel h2,
.publish-panel h3,
.publish-panel p,
.publish-panel dl,
.publish-panel dt,
.publish-panel dd,
.publish-panel ul {
  margin: 0;
}

.publish-panel button {
  min-height: var(--component-control-min-size);
  padding: var(--spacing-xs) var(--spacing-md);
  border: 1px solid var(--color-primary);
  border-radius: var(--rounded-pill);
  background: var(--color-canvas);
  color: var(--color-primary);
  cursor: pointer;
}

.publish-panel button[data-action='publish'] {
  justify-self: end;
  background: var(--color-primary);
  color: var(--color-body-on-dark);
}

.publish-panel button:disabled {
  border-color: var(--color-ink-muted-48);
  background: var(--color-canvas-parchment);
  color: var(--color-ink-muted-80);
  cursor: not-allowed;
}

.publish-panel__block {
  color: var(--color-state-info-foreground);
}

.publish-panel__error,
.publish-panel__invalid {
  color: var(--color-state-danger-foreground);
}

.publish-panel__invalid ul {
  max-height: var(--component-release-diagnostic-max-height);
  padding: 0;
  overflow: auto;
  list-style: none;
  font-family: var(--font-code);
}

.publish-panel__invalid li {
  padding: var(--spacing-xs);
  border-block-start: 1px solid var(--color-hairline);
  overflow-wrap: anywhere;
}

.publish-panel__unchanged {
  color: var(--color-state-success-foreground);
}

.publish-panel dl {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--spacing-xs);
}

.publish-panel dl div {
  min-width: 0;
  padding: var(--spacing-xs);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
}

.publish-panel dt {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
}

.publish-panel dd {
  overflow-wrap: anywhere;
}

@media (max-width: 734px) {
  .publish-panel header,
  .publish-panel dl {
    grid-template-columns: minmax(0, 1fr);
  }

  .publish-panel header button,
  .publish-panel button[data-action='publish'] {
    width: 100%;
    justify-self: stretch;
  }
}
</style>
