<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.1.0
-->
<script setup lang="ts">
import LoginForm from '../components/LoginForm.vue'
import type { SessionStore } from '../session'

interface Props {
  store?: SessionStore
}

const props = defineProps<Props>()
</script>

<template>
  <main
    class="login-view"
    aria-labelledby="login-title"
  >
    <section class="login-view__panel">
      <header class="login-view__header">
        <h1 id="login-title">
          登录 Nginx UIX
        </h1>
        <p>使用管理员凭据继续。</p>
      </header>
      <LoginForm :store="props.store" />
    </section>
  </main>
</template>

<style scoped>
.login-view {
  display: grid;
  min-height: 100vh;
  place-items: center;
  padding: var(--spacing-xl) var(--component-page-gutter);
  background: var(--color-canvas-parchment);
}

.login-view__panel {
  width: min(100%, calc(var(--spacing-section) * 6));
  padding: var(--spacing-xl);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  background: var(--color-canvas);
}

.login-view__header {
  margin-block-end: var(--spacing-xl);
}

.login-view__header h1 {
  margin-block-end: var(--spacing-xs);
  color: var(--color-ink);
  font-size: var(--font-size-tagline);
}

.login-view__header p {
  margin: 0;
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
  letter-spacing: var(--letter-spacing-caption);
}

@supports (min-height: 100dvh) {
  .login-view {
    min-height: 100dvh;
  }
}
</style>
