/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import { mount } from '@vue/test-utils'

import type { EffectiveConfigOccurrence } from '../api/types'
import { appI18n } from '../i18n'
import ConfigFileList from './ConfigFileList.vue'
import configFileListSource from './ConfigFileList.vue?raw'

const repeatedOccurrences: EffectiveConfigOccurrence[] = [
  {
    id: 'occurrence-000001',
    load_order: 1,
    path: '/etc/nginx/nginx.conf',
    content: 'events {}\n',
  },
  {
    id: 'occurrence-000002',
    load_order: 2,
    path: '/etc/nginx/conf.d/site.conf',
    content: 'server { listen 80; }\n',
  },
  {
    id: 'occurrence-000003',
    load_order: 3,
    path: '/etc/nginx/conf.d/site.conf',
    content: 'server { listen 8080; }\n',
  },
]

describe('ConfigFileList', () => {
  beforeEach(() => {
    appI18n.global.locale.value = 'zh-CN'
  })

  it('renders response order and distinguishes repeated paths by load occurrence', () => {
    const wrapper = mount(ConfigFileList, {
      props: { occurrences: repeatedOccurrences, selectedId: 'occurrence-000002' },
    })

    const rows = wrapper.get('ol').findAll('li')
    expect(rows.map((row) => row.text())).toEqual([
      '第 1 项/etc/nginx/nginx.conf',
      '第 2 项/etc/nginx/conf.d/site.conf',
      '第 3 项/etc/nginx/conf.d/site.conf',
    ])
    expect(rows.map((row) => row.get('button').attributes('aria-current'))).toEqual([
      undefined,
      'true',
      undefined,
    ])
  })

  it.each([
    ['Enter', 'occurrence-000001'],
    [' ', 'occurrence-000003'],
  ])('selects only the response-scoped id with the %s key', async (key, id) => {
    const wrapper = mount(ConfigFileList, {
      props: { occurrences: repeatedOccurrences, selectedId: 'occurrence-000002' },
      attachTo: document.body,
    })
    const button = wrapper.findAll('button').find((candidate) => candidate.attributes('data-id') === id)
    if (button === undefined) {
      throw new Error(`missing occurrence button ${id}`)
    }

    button.element.focus()
    expect(document.activeElement).toBe(button.element)
    await button.trigger('keydown', { key })

    expect(wrapper.emitted('select')).toEqual([[id]])
    wrapper.unmount()
  })

  it('uses ids as selector values and emits the selected id rather than a repeated path', async () => {
    const wrapper = mount(ConfigFileList, {
      props: { occurrences: repeatedOccurrences, selectedId: 'occurrence-000001' },
    })

    const selector = wrapper.get('select')
    expect(wrapper.get('label').attributes('for')).toBe(selector.attributes('id'))
    expect(selector.findAll('option').map((option) => option.attributes('value'))).toEqual(
      repeatedOccurrences.map((occurrence) => occurrence.id),
    )

    await selector.setValue('occurrence-000003')

    expect(wrapper.emitted('select')).toEqual([['occurrence-000003']])
  })

  it('uses the 833px responsive switch, tokenized chrome and 44px controls', () => {
    expect(configFileListSource).toContain('@media (max-width: 833px)')
    expect(configFileListSource).toContain('min-height: var(--component-control-min-size)')
    expect(configFileListSource).toContain('var(--color-primary)')
    expect(configFileListSource).not.toMatch(/#[\da-f]{3,8}\b/i)
    expect(configFileListSource).not.toMatch(/\b(?:linear|radial)-gradient\s*\(/)
    expect(configFileListSource).not.toContain('box-shadow')
    expect(configFileListSource).not.toMatch(/font-weight:\s*500\b/)
  })
})
