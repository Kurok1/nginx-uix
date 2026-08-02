<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.1.0
-->
<script setup lang="ts">
import { computed, inject, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { routerKey } from 'vue-router'

import { APIRequestError } from '../api/client'
import { formatAPIRequestError } from '../api/error_message'
import { sessionStore, type SessionStore } from '../session'
import FormField from './FormField.vue'

interface Props {
  store?: SessionStore
}

const props = defineProps<Props>()
const router = inject(routerKey, null)
const { t } = useI18n()

const username = ref('')
const password = ref('')
const submitting = ref(false)
const credentialsInvalid = ref(false)
const errorMessage = ref('')
const retryAfterSeconds = ref(0)
let retryTimer: ReturnType<typeof setInterval> | undefined

const describedBy = computed(() => {
  const ids: string[] = []
  if (errorMessage.value !== '') {
    ids.push('login-error')
  }
  if (retryAfterSeconds.value > 0) {
    ids.push('login-retry-status')
  }
  return ids.length === 0 ? undefined : ids.join(' ')
})

function stopRetryTimer(): void {
  if (retryTimer !== undefined) {
    clearInterval(retryTimer)
    retryTimer = undefined
  }
}

function startRetryTimer(seconds: number): void {
  stopRetryTimer()
  retryAfterSeconds.value = seconds
  retryTimer = setInterval(() => {
    retryAfterSeconds.value -= 1
    if (retryAfterSeconds.value <= 0) {
      stopRetryTimer()
      errorMessage.value = ''
    }
  }, 1000)
}

onUnmounted(stopRetryTimer)

async function handleSubmit(): Promise<void> {
  if (submitting.value || retryAfterSeconds.value > 0) {
    return
  }
  credentialsInvalid.value = false
  errorMessage.value = ''
  submitting.value = true
  try {
    await (props.store ?? sessionStore).login({
      username: username.value,
      password: password.value,
    })
    await router?.replace({ name: 'dashboard' })
  } catch (error: unknown) {
    if (error instanceof APIRequestError && error.apiError?.code === 'invalid_credentials') {
      credentialsInvalid.value = true
    }
    if (
      error instanceof APIRequestError &&
      error.apiError?.code === 'rate_limited' &&
      error.retryAfterSeconds !== undefined
    ) {
      errorMessage.value = formatAPIRequestError(error)
      startRetryTimer(error.retryAfterSeconds)
      return
    }
    if (error instanceof APIRequestError) {
      errorMessage.value = formatAPIRequestError(error)
      return
    }
    throw error
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <form
    class="login-form"
    :aria-busy="submitting"
    @submit.prevent="handleSubmit"
  >
    <FormField
      id="login-username"
      v-model="username"
      autocomplete="username"
      :described-by="describedBy"
      :invalid="credentialsInvalid"
      :label="t('auth.username')"
      name="username"
      :read-only="submitting"
      type="text"
    />
    <FormField
      id="login-password"
      v-model="password"
      autocomplete="current-password"
      :described-by="describedBy"
      :invalid="credentialsInvalid"
      :label="t('auth.password')"
      name="password"
      :read-only="submitting"
      type="password"
    />
    <p
      v-if="errorMessage !== ''"
      id="login-error"
      class="login-form__error"
      aria-live="polite"
      aria-atomic="true"
    >
      <svg
        class="login-form__error-icon"
        aria-hidden="true"
        focusable="false"
        viewBox="0 0 24 24"
      >
        <path
          d="M8.7 3h6.6L21 8.7v6.6L15.3 21H8.7L3 15.3V8.7L8.7 3Z"
          fill="none"
          stroke="currentColor"
          stroke-linejoin="round"
          stroke-width="2"
        />
        <path
          d="M12 7.5v6M12 17h.01"
          fill="none"
          stroke="currentColor"
          stroke-linecap="round"
          stroke-width="2"
        />
      </svg>
      <span>{{ errorMessage }}</span>
    </p>
    <p
      v-if="retryAfterSeconds > 0"
      id="login-retry-status"
      class="login-form__retry-status"
      aria-live="off"
    >
      {{ t('auth.retryAvailableIn', { seconds: retryAfterSeconds }, retryAfterSeconds) }}
    </p>
    <button
      type="submit"
      :disabled="submitting || retryAfterSeconds > 0"
    >
      {{ submitting
        ? t('auth.signingIn')
        : retryAfterSeconds > 0
          ? t('auth.retryIn', { seconds: retryAfterSeconds }, retryAfterSeconds)
          : t('auth.signIn') }}
    </button>
  </form>
</template>

<style scoped>
.login-form {
  display: grid;
  gap: var(--spacing-lg);
}

.login-form__error,
.login-form__retry-status {
  margin: 0;
  font-size: var(--font-size-caption);
  letter-spacing: var(--letter-spacing-caption);
}

.login-form__error {
  display: flex;
  align-items: center;
  gap: var(--spacing-xs);
  color: var(--color-status-error-foreground);
}

.login-form__error-icon {
  width: var(--spacing-md);
  height: var(--spacing-md);
  flex: none;
}

.login-form__retry-status {
  color: var(--color-ink-muted-80);
}

.login-form button[type='submit'] {
  width: 100%;
  min-height: var(--component-control-min-size);
  padding: var(--spacing-xs) var(--spacing-lg);
  border: 0;
  border-radius: var(--rounded-pill);
  background: var(--color-primary);
  color: var(--color-body-on-dark);
  font-weight: var(--font-weight-semibold);
  cursor: pointer;
}

.login-form button[type='submit']:disabled {
  cursor: not-allowed;
}
</style>
