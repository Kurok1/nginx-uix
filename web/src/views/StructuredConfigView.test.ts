/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */
import { flushPromises, mount } from '@vue/test-utils'

import type {
  StructuredChangePreview,
  StructuredChangeResult,
  StructuredConfig,
} from '../api/structured'
import type { WorkspaceDetail } from '../api/types'
import StructuredConfigView from './StructuredConfigView.vue'

const workspaceID = '0123456789abcdef0123456789abcdef'
const upstreamID = '1'.repeat(32)
const serverID = '2'.repeat(32)
const httpBlockID = '3'.repeat(32)
const httpServerID = '4'.repeat(32)
const locationID = '5'.repeat(32)
const digestA = 'a'.repeat(64)
const digestB = 'b'.repeat(64)
const draftETagA = `"draft-v1:${digestA}"`
const draftETagB = `"draft-v1:${digestB}"`
const source = {
  path: 'conf.d/upstreams.conf',
  start_line: 1,
  start_column: 1,
  end_line: 3,
  end_column: 2,
}

function workspace(etag = draftETagA): WorkspaceDetail {
  return {
    id: workspaceID,
    name: 'Structured draft',
    state: 'ready',
    production_digest: digestA,
    base_digest: digestA,
    draft_etag: etag,
    entry_count: 2,
    managed_bytes: 256,
    workspace_bytes: 512,
    created_by: 7,
    created_at: '2026-07-20T08:00:00Z',
    updated_at: '2026-07-20T08:01:00Z',
  }
}

function catalog(etag = draftETagA): StructuredConfig {
  return {
    workspace_id: workspaceID,
    draft_etag: etag,
    complete: true,
    project_diagnostics: [],
    http_blocks: [
      {
        id: httpBlockID,
        source: { ...source, path: 'nginx.conf' },
        editable: true,
        instances: 1,
      },
    ],
    upstreams: [
      {
        id: upstreamID,
        name: etag === draftETagA ? 'backend' : 'application',
        source,
        servers: [
          {
            id: serverID,
            source,
            endpoint: { address: '127.0.0.1', port: 8080, unix: false },
            weight: null,
            backup: false,
            down: false,
            max_fails: null,
            fail_timeout: null,
            preserved_parameters: [],
            editable: true,
          },
        ],
        preserved_directives: [],
        references: [],
        editable: true,
        instances: 1,
      },
    ],
    proxy_pass_references: [],
    servers: [
      {
        id: httpServerID,
        source: { ...source, path: 'conf.d/site.conf' },
        listens: ['80'],
        server_names: ['example.test'],
        summary_truncated: false,
        locations: [
          {
            id: locationID,
            type: 'prefix',
            matcher: '/api',
            source: { ...source, path: 'conf.d/site.conf' },
            children: [],
            proxy_passes: [],
            unknown_directive_count: 0,
            editable: true,
            proxy_pass_editable: true,
            instances: 1,
          },
        ],
        editable: true,
        instances: 1,
      },
    ],
    diagnostics: [],
  }
}

const preview: StructuredChangePreview = {
  preview_id: digestB,
  workspace_id: workspaceID,
  draft_etag: draftETagA,
  operation_kind: 'upstream.rename',
  target_id: upstreamID,
  changed_files: [
    {
      path: source.path,
      before_digest: digestA,
      after_digest: digestB,
      added_lines: 1,
      removed_lines: 1,
      patch: '@@ -1 +1 @@\n-upstream backend {\n+upstream application {\n',
    },
  ],
  complete: true,
}

function result(): StructuredChangeResult {
  return {
    workspace: workspace(draftETagB),
    draft_etag: draftETagB,
    changed_paths: [source.path],
  }
}

describe('StructuredConfigView', () => {
  it('loads an ETag-consistent workspace and keeps the raw editor fallback visible', async () => {
    const client = {
      getWorkspace: vi.fn().mockResolvedValue(workspace()),
      getStructuredConfig: vi.fn().mockResolvedValue(catalog()),
      previewStructuredChange: vi.fn(),
      applyStructuredChange: vi.fn(),
    }
    const wrapper = mount(StructuredConfigView, {
      props: {
        workspaceId: workspaceID,
        mode: 'upstreams',
        client,
        csrfToken: 'csrf-token',
      },
      global: {
        stubs: {
          RouterLink: {
            props: ['to'],
            template: '<a :href="typeof to === \'string\' ? to : to.path"><slot /></a>',
          },
        },
      },
    })
    await flushPromises()

    expect(wrapper.text()).toContain('Structured draft')
    expect(wrapper.text()).toContain('Draft only — full Nginx validation has not run')
    expect(wrapper.text()).toContain('backend')
    expect(wrapper.get('[data-fallback="raw-editor"]').attributes('href')).toBe(
      '/config/workspaces/' + workspaceID,
    )
  })

  it('previews, confirms and applies exactly one operation before refreshing the ETag', async () => {
    const client = {
      getWorkspace: vi
        .fn()
        .mockResolvedValueOnce(workspace())
        .mockResolvedValueOnce(workspace(draftETagB)),
      getStructuredConfig: vi
        .fn()
        .mockResolvedValueOnce(catalog())
        .mockResolvedValueOnce(catalog(draftETagB)),
      previewStructuredChange: vi.fn().mockResolvedValue(preview),
      applyStructuredChange: vi.fn().mockResolvedValue(result()),
    }
    const wrapper = mount(StructuredConfigView, {
      props: {
        workspaceId: workspaceID,
        mode: 'upstreams',
        client,
        csrfToken: 'csrf-token',
      },
      global: {
        stubs: {
          RouterLink: {
            props: ['to'],
            template: '<a :href="typeof to === \'string\' ? to : to.path"><slot /></a>',
          },
        },
      },
    })
    await flushPromises()

    await wrapper.get('[name="upstream-name"]').setValue('application')
    await wrapper.get('[data-action="review-upstream"]').trigger('click')
    await flushPromises()

    expect(client.previewStructuredChange).toHaveBeenCalledWith(
      workspaceID,
      {
        kind: 'upstream.rename',
        input: { upstream_id: upstreamID, new_name: 'application' },
      },
      'csrf-token',
      expect.any(AbortSignal),
    )
    await wrapper
      .get('[aria-label="Type application exactly to confirm"]')
      .setValue('application')
    await wrapper.get('[data-action="apply"]').trigger('click')
    await flushPromises()

    expect(client.applyStructuredChange).toHaveBeenCalledWith(
      workspaceID,
      {
        kind: 'upstream.rename',
        input: { upstream_id: upstreamID, new_name: 'application' },
      },
      digestB,
      draftETagA,
      'csrf-token',
      expect.any(AbortSignal),
    )
    expect(wrapper.text()).toContain('application')
    expect(wrapper.text()).toContain('Draft updated')
  })

  it('invalidates a generated preview as soon as the form changes again', async () => {
    const client = {
      getWorkspace: vi.fn().mockResolvedValue(workspace()),
      getStructuredConfig: vi.fn().mockResolvedValue(catalog()),
      previewStructuredChange: vi.fn().mockResolvedValue(preview),
      applyStructuredChange: vi.fn(),
    }
    const wrapper = mount(StructuredConfigView, {
      props: {
        workspaceId: workspaceID,
        mode: 'upstreams',
        client,
        csrfToken: 'csrf-token',
      },
      global: {
        stubs: {
          RouterLink: {
            props: ['to'],
            template: '<a :href="typeof to === \'string\' ? to : to.path"><slot /></a>',
          },
        },
      },
    })
    await flushPromises()

    await wrapper.get('[name="upstream-name"]').setValue('application')
    await wrapper.get('[data-action="review-upstream"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('.structured-change-review__diff').exists()).toBe(true)

    await wrapper.get('[name="upstream-name"]').setValue('next')

    expect(wrapper.find('.structured-change-review__diff').exists()).toBe(false)
    expect(wrapper.text()).toContain('Choose an edit and generate a preview before applying it.')
    expect(client.applyStructuredChange).not.toHaveBeenCalled()
  })

  it('keeps both server and location selectors available for compact layouts', async () => {
    const client = {
      getWorkspace: vi.fn().mockResolvedValue(workspace()),
      getStructuredConfig: vi.fn().mockResolvedValue(catalog()),
      previewStructuredChange: vi.fn(),
      applyStructuredChange: vi.fn(),
    }
    const wrapper = mount(StructuredConfigView, {
      props: {
        workspaceId: workspaceID,
        mode: 'servers',
        client,
        csrfToken: 'csrf-token',
      },
      global: {
        stubs: {
          RouterLink: {
            props: ['to'],
            template: '<a :href="typeof to === \'string\' ? to : to.path"><slot /></a>',
          },
        },
      },
    })
    await flushPromises()

    expect(wrapper.get('[data-structured-selector="server"]').text()).toContain('example.test')
    expect(wrapper.get('[data-structured-selector="location"]').text()).toContain('/api')
  })

  it('keeps unrelated complete resources editable when another node has a blocking diagnostic', async () => {
    const projected = catalog()
    projected.diagnostics.push({
      domain: 'location',
      code: 'location_invalid_matcher',
      severity: 'blocking',
      source: { ...source, path: 'conf.d/unrelated.conf' },
      related_id: '9'.repeat(32),
    })
    const client = {
      getWorkspace: vi.fn().mockResolvedValue(workspace()),
      getStructuredConfig: vi.fn().mockResolvedValue(projected),
      previewStructuredChange: vi.fn().mockResolvedValue(preview),
      applyStructuredChange: vi.fn(),
    }
    const wrapper = mount(StructuredConfigView, {
      props: {
        workspaceId: workspaceID,
        mode: 'upstreams',
        client,
        csrfToken: 'csrf-token',
      },
      global: {
        stubs: {
          RouterLink: {
            props: ['to'],
            template: '<a :href="typeof to === \'string\' ? to : to.path"><slot /></a>',
          },
        },
      },
    })
    await flushPromises()

    await wrapper.get('[name="upstream-name"]').setValue('application')
    await wrapper.get('[data-action="review-upstream"]').trigger('click')
    await flushPromises()

    expect(client.previewStructuredChange).toHaveBeenCalledOnce()
  })

  it('keeps the browse task visible during listbox keyboard navigation', async () => {
    const projected = catalog()
    projected.upstreams.push({
      ...projected.upstreams[0]!,
      id: '6'.repeat(32),
      name: 'secondary',
      servers: [],
      references: [],
    })
    const client = {
      getWorkspace: vi.fn().mockResolvedValue(workspace()),
      getStructuredConfig: vi.fn().mockResolvedValue(projected),
      previewStructuredChange: vi.fn(),
      applyStructuredChange: vi.fn(),
    }
    const wrapper = mount(StructuredConfigView, {
      props: {
        workspaceId: workspaceID,
        mode: 'upstreams',
        client,
        csrfToken: 'csrf-token',
      },
      global: {
        stubs: {
          RouterLink: {
            props: ['to'],
            template: '<a :href="typeof to === \'string\' ? to : to.path"><slot /></a>',
          },
        },
      },
    })
    await flushPromises()

    await wrapper.get('[role="option"]').trigger('keydown', { key: 'ArrowDown' })

    expect(wrapper.get('.structured-workbench__browse').classes()).toContain(
      'structured-task-panel--active',
    )
    expect(wrapper.get('.structured-workbench__detail').classes()).not.toContain(
      'structured-task-panel--active',
    )
  })
})
