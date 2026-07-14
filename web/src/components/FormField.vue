<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.1.0
-->
<script setup lang="ts">
interface Props {
  autocomplete: 'current-password' | 'username'
  describedBy?: string
  disabled?: boolean
  id: string
  label: string
  modelValue: string
  name: string
  invalid?: boolean
  type: 'password' | 'text'
}

defineProps<Props>()

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

function handleInput(event: Event): void {
  if (event.target instanceof HTMLInputElement) {
    emit('update:modelValue', event.target.value)
  }
}
</script>

<template>
  <div class="form-field">
    <label
      class="form-field__label"
      :for="id"
    >{{ label }}</label>
    <input
      :id="id"
      class="form-field__control"
      :name="name"
      :type="type"
      :autocomplete="autocomplete"
      :aria-describedby="describedBy"
      :aria-invalid="invalid || undefined"
      :disabled="disabled"
      :value="modelValue"
      @input="handleInput"
    >
  </div>
</template>

<style scoped>
.form-field {
  display: grid;
  gap: var(--spacing-xs);
}

.form-field__label {
  color: var(--color-ink);
  font-size: var(--font-size-caption);
  font-weight: var(--font-weight-semibold);
  letter-spacing: var(--letter-spacing-caption);
}

.form-field__control {
  width: 100%;
  min-height: var(--component-control-min-size);
  padding: var(--spacing-xs) var(--spacing-sm);
  border: 1px solid var(--color-ink-muted-48);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas);
  color: var(--color-ink);
}

.form-field__control[aria-invalid='true'] {
  border-color: var(--color-status-error-foreground);
}

.form-field__control:disabled {
  border-color: var(--color-ink-muted-48);
  background: var(--color-canvas-parchment);
  color: var(--color-ink-muted-80);
  cursor: not-allowed;
}
</style>
