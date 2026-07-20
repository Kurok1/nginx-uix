/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */
import { APIClient } from './client'
import {
  parseStructuredChangePreview,
  parseStructuredConfig,
  type StructuredOperation,
} from './structured'

const workspaceID = '0123456789abcdef0123456789abcdef'
const objectID = '1'.repeat(32)
const upstreamID = '2'.repeat(32)
const serverID = '3'.repeat(32)
const locationID = '4'.repeat(32)
const referenceID = '5'.repeat(32)
const digestA = 'a'.repeat(64)
const digestB = 'b'.repeat(64)
const draftETagA = `"draft-v1:${digestA}"`
const draftETagB = `"draft-v1:${digestB}"`

const source = {
  path: 'conf.d/site.conf',
  start_line: 2,
  start_column: 3,
  end_line: 8,
  end_column: 4,
}

function catalogFixture() {
  return {
    workspace_id: workspaceID,
    draft_etag: draftETagA,
    complete: true,
    project_diagnostics: [],
    http_blocks: [
      {
        id: objectID,
        source: { ...source, path: 'nginx.conf' },
        editable: true,
        instances: 1,
      },
    ],
    upstreams: [
      {
        id: upstreamID,
        name: 'backend',
        source,
        servers: [
          {
            id: objectID,
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
            id: referenceID,
            source,
            state: 'resolved',
            scheme: 'http',
            host: 'backend',
            port: null,
            uri: '/api',
            upstream_id: upstreamID,
            upstream_name: 'backend',
          },
        ],
        editable: true,
        instances: 1,
      },
    ],
    proxy_pass_references: [],
    servers: [
      {
        id: serverID,
        source,
        listens: ['80'],
        server_names: ['example.test'],
        summary_truncated: false,
        locations: [
          {
            id: locationID,
            type: 'prefix',
            matcher: '/api',
            source,
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
    diagnostics: [
      {
        domain: 'location',
        code: 'location_regex_order_sensitive',
        severity: 'warning',
        source,
        related_id: locationID,
        parent_id: serverID,
      },
    ],
  }
}

function workspaceFixture() {
  return {
    id: workspaceID,
    name: 'Structured draft',
    state: 'ready',
    production_digest: digestA,
    base_digest: digestA,
    draft_etag: draftETagB,
    entry_count: 2,
    managed_bytes: 256,
    workspace_bytes: 512,
    created_by: 7,
    created_at: '2026-07-20T08:00:00Z',
    updated_at: '2026-07-20T08:01:00Z',
  }
}

const renameOperation: StructuredOperation = {
  kind: 'upstream.rename',
  input: { upstream_id: upstreamID, new_name: 'application' },
}

describe('structured API response boundary', () => {
  it('parses the bounded catalog without exposing preserved raw syntax', () => {
    const catalog = parseStructuredConfig(catalogFixture(), 200)

    expect(catalog.upstreams[0]?.servers[0]?.preserved_parameters).toEqual([
      { name: 'resolve', editable: false },
    ])
    expect(catalog.servers[0]?.locations[0]?.matcher).toBe('/api')
    expect(catalog.diagnostics[0]?.severity).toBe('warning')

    const malformed = catalogFixture()
    Object.assign(malformed.upstreams[0]!.preserved_directives[0]!, { raw: 'keepalive 32;' })
    expect(() => parseStructuredConfig(malformed, 200)).toThrowError(
      expect.objectContaining({ kind: 'malformed_response' }),
    )
  })

  it('parses complete preview evidence and rejects an inconsistent empty change set', () => {
    const preview = {
      preview_id: digestB,
      workspace_id: workspaceID,
      draft_etag: draftETagA,
      operation_kind: 'upstream.rename',
      target_id: upstreamID,
      changed_files: [
        {
          path: 'conf.d/site.conf',
          before_digest: digestA,
          after_digest: digestB,
          added_lines: 2,
          removed_lines: 2,
          patch: '@@ -1 +1 @@\n-upstream backend {\n+upstream application {\n',
        },
      ],
      complete: true,
    }

    expect(parseStructuredChangePreview(preview, 200).changed_files).toHaveLength(1)
    expect(() =>
      parseStructuredChangePreview({ ...preview, changed_files: [] }, 200),
    ).toThrowError(expect.objectContaining({ kind: 'malformed_response' }))
  })

  it('accepts responses up to parser bounds instead of smaller UI-only limits', () => {
    const fixture = catalogFixture()
    fixture.http_blocks = Array.from({ length: 65 }, (_, index) => ({
      ...fixture.http_blocks[0]!,
      id: index.toString(16).padStart(32, '0'),
    }))
    fixture.upstreams[0]!.servers[0]!.preserved_parameters = Array.from(
      { length: 65 },
      (_, index) => ({ name: 'parameter_' + String(index), editable: false as const }),
    )

    expect(parseStructuredConfig(fixture, 200).http_blocks).toHaveLength(65)
  })

  it('rejects non-portable source path components and nul bytes', () => {
    const oversized = catalogFixture()
    oversized.upstreams[0]!.source = {
      ...oversized.upstreams[0]!.source,
      path: 'a'.repeat(256),
    }
    expect(() => parseStructuredConfig(oversized, 200)).toThrowError(
      expect.objectContaining({ kind: 'malformed_response' }),
    )

    const nul = catalogFixture()
    nul.upstreams[0]!.source = {
      ...nul.upstreams[0]!.source,
      path: 'conf.d/site\0.conf',
    }
    expect(() => parseStructuredConfig(nul, 200)).toThrowError(
      expect.objectContaining({ kind: 'malformed_response' }),
    )
  })
})

describe('structured API client', () => {
  it('binds catalog and preview responses to their ETag and sends same-origin mutation headers', async () => {
    const preview = {
      preview_id: digestB,
      workspace_id: workspaceID,
      draft_etag: draftETagA,
      operation_kind: renameOperation.kind,
      target_id: upstreamID,
      changed_files: [
        {
          path: 'conf.d/site.conf',
          before_digest: digestA,
          after_digest: digestB,
          added_lines: 1,
          removed_lines: 1,
          patch: 'diff',
        },
      ],
      complete: true,
    }
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        new Response(JSON.stringify(catalogFixture()), {
          status: 200,
          headers: { 'Content-Type': 'application/json', ETag: draftETagA },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify(preview), {
          status: 200,
          headers: { 'Content-Type': 'application/json', ETag: draftETagA },
        }),
      )
    const client = new APIClient(fetcher)

    await expect(client.getStructuredConfig(workspaceID)).resolves.toMatchObject({
      workspace_id: workspaceID,
      complete: true,
    })
    await expect(
      client.previewStructuredChange(workspaceID, renameOperation, 'csrf-token'),
    ).resolves.toMatchObject({ operation_kind: 'upstream.rename' })

    expect(fetcher).toHaveBeenNthCalledWith(
      2,
      `/api/v1/config/workspaces/${workspaceID}/structured-change-previews`,
      expect.objectContaining({
        method: 'POST',
        credentials: 'same-origin',
        cache: 'no-store',
        headers: expect.objectContaining({
          'Content-Type': 'application/json',
          'X-CSRF-Token': 'csrf-token',
        }),
        body: JSON.stringify(renameOperation),
      }),
    )
  })

  it('applies only the exact preview and revision and verifies the returned ETag', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({
          workspace: workspaceFixture(),
          draft_etag: draftETagB,
          changed_paths: ['conf.d/site.conf'],
        }),
        {
          status: 200,
          headers: { 'Content-Type': 'application/json', ETag: draftETagB },
        },
      ),
    )
    const client = new APIClient(fetcher)

    await expect(
      client.applyStructuredChange(
        workspaceID,
        renameOperation,
        digestB,
        draftETagA,
        'csrf-token',
      ),
    ).resolves.toMatchObject({ draft_etag: draftETagB })
    expect(fetcher).toHaveBeenCalledWith(
      `/api/v1/config/workspaces/${workspaceID}/structured-changes`,
      expect.objectContaining({
        headers: expect.objectContaining({
          'If-Match': draftETagA,
          'X-CSRF-Token': 'csrf-token',
        }),
        body: JSON.stringify({ preview_id: digestB, ...renameOperation }),
      }),
    )
  })
})
