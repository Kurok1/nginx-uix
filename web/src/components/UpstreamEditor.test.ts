/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */
import { mount } from '@vue/test-utils'

import type { StructuredHTTPBlock, StructuredUpstream } from '../api/structured'
import UpstreamEditor from './UpstreamEditor.vue'

const source = {
  path: 'conf.d/upstreams.conf',
  start_line: 1,
  start_column: 1,
  end_line: 3,
  end_column: 2,
}
const httpBlocks: StructuredHTTPBlock[] = [
  { id: '1'.repeat(32), source, editable: true, instances: 1 },
]
const upstream: StructuredUpstream = {
  id: '2'.repeat(32),
  name: 'backend',
  source,
  servers: [
    {
      id: '3'.repeat(32),
      source,
      endpoint: { address: '127.0.0.1', port: 8080, unix: false },
      weight: 2,
      backup: false,
      down: false,
      max_fails: 3,
      fail_timeout: '10s',
      preserved_parameters: [{ name: 'resolve', editable: false }],
      editable: true,
    },
  ],
  preserved_directives: [{ name: 'keepalive', editable: false }],
  references: [
    {
      id: '4'.repeat(32),
      source: { ...source, path: 'conf.d/site.conf', start_line: 12, start_column: 7 },
      state: 'resolved',
      scheme: 'http',
      host: 'backend',
      port: null,
      upstream_id: '2'.repeat(32),
      upstream_name: 'backend',
    },
  ],
  editable: true,
  instances: 1,
}

describe('UpstreamEditor', () => {
  it('previews a rename and identifies preserved syntax as read only', async () => {
    const wrapper = mount(UpstreamEditor, {
      props: { upstream, httpBlocks, disabled: false },
    })

    expect(wrapper.text()).toContain('keepalive')
    expect(wrapper.text()).toContain('resolve')
    expect(wrapper.text()).toContain('conf.d/site.conf:12:7')
    await wrapper.get('[name="upstream-name"]').setValue('application')
    await wrapper.get('[data-action="review-upstream"]').trigger('click')

    expect(wrapper.emitted('preview')?.[0]).toEqual([
      {
        kind: 'upstream.rename',
        input: { upstream_id: upstream.id, new_name: 'application' },
      },
    ])
    expect(wrapper.emitted('dirty-change')?.at(-1)).toEqual([true])
  })

  it('previews a typed server update without losing supported parameters', async () => {
    const wrapper = mount(UpstreamEditor, {
      props: { upstream, httpBlocks, disabled: false },
    })

    await wrapper.get('[name="server-weight"]').setValue('4')
    await wrapper.get('[data-action="review-server"]').trigger('click')

    expect(wrapper.emitted('preview')?.[0]).toEqual([
      {
        kind: 'upstream_server.update',
        input: {
          upstream_id: upstream.id,
          server_id: upstream.servers[0]!.id,
          server: {
            address: '127.0.0.1',
            port: 8080,
            unix: false,
            weight: 4,
            backup: false,
            down: false,
            max_fails: 3,
            fail_timeout: '10s',
          },
        },
      },
    ])
  })

  it('does not discard a dirty server form when switching peers or operations', async () => {
    const second = {
      ...upstream.servers[0]!,
      id: '5'.repeat(32),
      endpoint: { address: '127.0.0.2', port: 8081, unix: false },
    }
    const wrapper = mount(UpstreamEditor, {
      props: {
        upstream: { ...upstream, servers: [...upstream.servers, second] },
        httpBlocks,
        disabled: false,
      },
    })

    await wrapper.get('[name="server-weight"]').setValue('4')

    expect(wrapper.get('[name="upstream-server"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-action="add-server"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-action="delete-server"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-action="delete-upstream"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[name="upstream-name"]').attributes('disabled')).toBeDefined()

    await wrapper.get('[data-action="reset-server"]').trigger('click')
    expect(wrapper.get('[name="server-weight"]').element).toHaveProperty('value', '2')
    expect(wrapper.get('[name="upstream-server"]').attributes('disabled')).toBeUndefined()
  })

  it('prevents a name edit and server edit from producing competing previews', async () => {
    const wrapper = mount(UpstreamEditor, {
      props: { upstream, httpBlocks, disabled: false },
    })

    await wrapper.get('[name="upstream-name"]').setValue('application')
    expect(wrapper.get('[data-action="add-server"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-action="review-server"]').attributes('disabled')).toBeDefined()

    await wrapper.get('[data-action="reset-upstream-name"]').trigger('click')
    expect(wrapper.get('[name="upstream-name"]').element).toHaveProperty('value', 'backend')
    expect(wrapper.get('[data-action="add-server"]').attributes('disabled')).toBeUndefined()
  })
})
