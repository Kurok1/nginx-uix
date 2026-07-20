/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { mount } from '@vue/test-utils'

import InlineBanner from './InlineBanner.vue'
import bannerSource from './InlineBanner.vue?raw'

const baseSource = readFileSync(resolve(process.cwd(), 'src/styles/base.css'), 'utf8')
const tokensSource = readFileSync(resolve(process.cwd(), 'src/styles/tokens.css'), 'utf8')

describe('InlineBanner', () => {
  it.each([
    ['info', 'status'],
    ['stale', 'status'],
    ['conflict', 'alert'],
    ['needs_attention', 'alert'],
    ['agent', 'alert'],
  ] as const)('renders %s with the required %s role and a text plus icon cue', (kind, role) => {
    const wrapper = mount(InlineBanner, {
      props: { kind, message: `Visible ${kind} message` },
    })
    const banner = wrapper.get(`div[role="${role}"]`)

    expect(banner.text()).toContain(`Visible ${kind} message`)
    const icon = banner.get(`[data-icon="${kind}"]`)
    expect(icon.attributes('aria-hidden')).toBe('true')
    expect(icon.text()).not.toBe('')
  })

  it('renders persistent contextual actions in its action slot', async () => {
    const wrapper = mount(InlineBanner, {
      props: {
        kind: 'conflict',
        message: 'This file changed on the server. Your local text has not been overwritten.',
      },
      slots: {
        actions:
          '<button type="button">Copy local content</button><button type="button">Read server version</button>',
      },
    })
    const buttons = wrapper.findAll('button')

    expect(buttons.map((button) => button.text())).toEqual([
      'Copy local content',
      'Read server version',
    ])
    await buttons[0]?.trigger('click')
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
  })

  it('sizes slotted actions and uses only centralized semantic variables', () => {
    expect(bannerSource).toContain('min-height: var(--component-control-min-size)')
    expect(bannerSource).toContain('var(--color-state-info)')
    expect(bannerSource).toContain('var(--color-state-warning)')
    expect(bannerSource).toContain('var(--color-state-danger)')
    expect(bannerSource).not.toMatch(/#[\da-f]{3,8}\b/i)
    expect(bannerSource).not.toMatch(/\b(?:linear|radial)-gradient\s*\(/)
    expect(bannerSource).not.toContain('box-shadow')
  })

  it('maps the stable workspace tokens and global inert/scroll behavior centrally', () => {
    expect(tokensSource).toContain('--color-state-success:')
    expect(tokensSource).toContain('--color-state-warning:')
    expect(tokensSource).toContain('--color-state-danger:')
    expect(tokensSource).toContain('--color-state-info:')
    expect(tokensSource).toContain('--color-diff-added:')
    expect(tokensSource).toContain('--color-diff-removed:')
    expect(tokensSource).toContain('--color-diff-context:')
    expect(tokensSource).toContain('--component-workspace-tree-width: 240px')
    expect(tokensSource).toContain('--component-workspace-tree-width-narrow: 208px')
    expect(tokensSource).toContain('--component-workspace-review-width: 360px')
    expect(tokensSource).toContain('--component-workspace-header-min-height: 56px')
    expect(tokensSource).toContain('--component-editor-min-height: 480px')
    expect(tokensSource).toContain('--component-drawer-width: min(92vw, 520px)')
    expect(tokensSource).toContain(
      '--component-modal-width: min(calc(100vw - 32px), 480px)',
    )
    expect(baseSource).toMatch(/\[inert\]\s*\{/)
    expect(baseSource).toContain('scrollbar-gutter: stable')
  })
})
