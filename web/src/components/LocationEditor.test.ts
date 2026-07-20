/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */
import { mount } from '@vue/test-utils'

import type {
  StructuredHTTPServer,
  StructuredLocation,
  StructuredUpstream,
} from '../api/structured'
import LocationEditor from './LocationEditor.vue'

const source = {
  path: 'conf.d/site.conf',
  start_line: 1,
  start_column: 1,
  end_line: 5,
  end_column: 2,
}
const upstream: StructuredUpstream = {
  id: '1'.repeat(32),
  name: 'backend',
  source,
  servers: [],
  preserved_directives: [],
  references: [],
  editable: true,
  instances: 1,
}
const location: StructuredLocation = {
  id: '2'.repeat(32),
  type: 'prefix',
  matcher: '/api',
  source,
  children: [],
  proxy_passes: [],
  unknown_directive_count: 1,
  editable: true,
  proxy_pass_editable: true,
  instances: 1,
}
const server: StructuredHTTPServer = {
  id: '3'.repeat(32),
  source,
  listens: ['80'],
  server_names: ['example.test'],
  summary_truncated: false,
  locations: [location],
  editable: true,
  instances: 1,
}

describe('LocationEditor', () => {
  it('previews a matcher and proxy_pass update with an explicit proxy mode', async () => {
    const wrapper = mount(LocationEditor, {
      props: { server, location, upstreams: [upstream], disabled: false },
    })

    expect(wrapper.text()).toContain('1 preserved directive')
    await wrapper.get('[name="location-matcher"]').setValue('/service')
    await wrapper.get('[name="proxy-mode"]').setValue('set')
    await wrapper.get('[name="proxy-upstream"]').setValue(upstream.id)
    await wrapper.get('[data-action="review-location"]').trigger('click')

    expect(wrapper.emitted('preview')?.[0]).toEqual([
      {
        kind: 'location.update',
        input: {
          location_id: location.id,
          type: 'prefix',
          matcher: '/service',
          proxy_mode: 'set',
          proxy_pass: {
            upstream_id: upstream.id,
            scheme: 'http',
            port: null,
            uri: '',
          },
        },
      },
    ])
  })

  it('previews creation beneath the selected server', async () => {
    const wrapper = mount(LocationEditor, {
      props: { server, location: null, upstreams: [upstream], disabled: false },
    })

    await wrapper.get('[data-action="add-root"]').trigger('click')
    await wrapper.get('[name="location-matcher"]').setValue('/new')
    await wrapper.get('[data-action="review-location"]').trigger('click')

    expect(wrapper.emitted('preview')?.[0]).toEqual([
      {
        kind: 'location.create',
        input: {
          parent_id: server.id,
          type: 'prefix',
          matcher: '/new',
          proxy_pass: null,
        },
      },
    ])
  })

  it('keeps dirty matcher values until they are explicitly reset', async () => {
    const wrapper = mount(LocationEditor, {
      props: { server, location, upstreams: [upstream], disabled: false },
    })

    await wrapper.get('[name="location-matcher"]').setValue('/service')

    expect(wrapper.get('[data-action="add-root"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-action="add-child"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-action="delete-location"]').attributes('disabled')).toBeDefined()

    await wrapper.get('[data-action="reset-location"]').trigger('click')
    expect(wrapper.get('[name="location-matcher"]').element).toHaveProperty('value', '/api')
    expect(wrapper.get('[data-action="add-root"]').attributes('disabled')).toBeUndefined()
  })
})
