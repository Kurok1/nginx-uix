<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.2.1
-->
<template>
  <div
    ref="host"
    class="code-editor"
  />
</template>

<script setup lang="ts">
import { defaultKeymap, history, historyKeymap } from '@codemirror/commands'
import { StreamLanguage } from '@codemirror/language'
import { nginx } from '@codemirror/legacy-modes/mode/nginx'
import { searchKeymap } from '@codemirror/search'
import { Compartment, EditorState } from '@codemirror/state'
import { EditorView, keymap, lineNumbers } from '@codemirror/view'
import { onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'

interface Props {
  ariaLabel: string
  modelValue: string
  readOnly: boolean
}

const props = defineProps<Props>()
const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

const host = ref<HTMLElement | null>(null)
const editorView = shallowRef<EditorView | null>(null)
const readOnlyCompartment = new Compartment()
const lineSeparatorCompartment = new Compartment()
const ariaLabelCompartment = new Compartment()
let applyingExternalValue = false

function bootstrapCSPNonce(): string {
  return document
    .querySelector<HTMLMetaElement>('meta[name="nginx-uix-csp-nonce"]')
    ?.content.trim() ?? ''
}

function lineSeparator(value: string): '\r\n' | '\n' {
  return value.includes('\r\n') ? '\r\n' : '\n'
}

onMounted(() => {
  if (host.value === null) {
    return
  }

  editorView.value = new EditorView({
    parent: host.value,
    state: EditorState.create({
      doc: props.modelValue,
      extensions: [
        lineNumbers(),
        history(),
        keymap.of([...defaultKeymap, ...historyKeymap, ...searchKeymap]),
        StreamLanguage.define(nginx),
        EditorView.cspNonce.of(bootstrapCSPNonce()),
        readOnlyCompartment.of(EditorState.readOnly.of(props.readOnly)),
        lineSeparatorCompartment.of(
          EditorState.lineSeparator.of(lineSeparator(props.modelValue)),
        ),
        ariaLabelCompartment.of(
          EditorView.contentAttributes.of({ 'aria-label': props.ariaLabel }),
        ),
        EditorView.updateListener.of((update) => {
          if (update.docChanged && !applyingExternalValue) {
            emit('update:modelValue', update.state.sliceDoc())
          }
        }),
        EditorView.theme({
          '&': { height: '100%' },
          '.cm-scroller': {
            overflow: 'auto',
            fontFamily: 'var(--font-code)',
          },
          '&.cm-focused': { outline: 'var(--focus-outline)' },
        }),
      ],
    }),
  })
})

watch(
  () => props.modelValue,
  (value) => {
    const view = editorView.value
    if (view === null || value === view.state.sliceDoc()) {
      return
    }

    applyingExternalValue = true
    try {
      view.dispatch({
        changes: { from: 0, to: view.state.doc.length, insert: value },
        effects: lineSeparatorCompartment.reconfigure(
          EditorState.lineSeparator.of(lineSeparator(value)),
        ),
      })
    } finally {
      applyingExternalValue = false
    }
  },
)

watch(
  () => props.readOnly,
  (readOnly) => {
    editorView.value?.dispatch({
      effects: readOnlyCompartment.reconfigure(EditorState.readOnly.of(readOnly)),
    })
  },
)

watch(
  () => props.ariaLabel,
  (ariaLabel) => {
    editorView.value?.dispatch({
      effects: ariaLabelCompartment.reconfigure(
        EditorView.contentAttributes.of({ 'aria-label': ariaLabel }),
      ),
    })
  },
)

onBeforeUnmount(() => {
  editorView.value?.destroy()
  editorView.value = null
})

function editorViewForTest(): EditorView {
  if (editorView.value === null) {
    throw new Error('editor view is not mounted')
  }
  return editorView.value
}

defineExpose({ editorViewForTest })
</script>

<style scoped>
.code-editor {
  min-width: 0;
  min-height: var(--component-editor-min-height);
  overflow: hidden;
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas);
}

.code-editor :deep(.cm-editor) {
  min-width: 0;
  min-height: var(--component-editor-min-height);
}

.code-editor :deep(.cm-scroller) {
  min-width: 0;
}
</style>
