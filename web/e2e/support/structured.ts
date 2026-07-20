/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */
import { expect, type Page, type Route } from '@playwright/test'

import type {
  StructuredChangePreview,
  StructuredConfig,
  StructuredLocation,
  StructuredOperation,
} from '../../src/api/structured'
import {
  appOrigin,
  csrfToken,
  sessionCookieName,
  sessionCookieValue,
  type WorkspaceAPIFixture,
} from './api'

export const structuredHTTPBlockID = '1'.repeat(32)
export const structuredUpstreamID = '2'.repeat(32)
export const structuredPeerID = '3'.repeat(32)
export const structuredServerID = '4'.repeat(32)
export const structuredPreviewID = 'b'.repeat(64)

export interface StructuredAPIRequest {
  method: string
  path: string
  headers: Readonly<Record<string, string>>
  body: unknown
}

export interface StructuredAPIFixture {
  requests: () => readonly StructuredAPIRequest[]
  assertContract: () => void
}

export async function installStructuredAPIFixture(
  page: Page,
  workspace: WorkspaceAPIFixture,
  options: { incomplete?: boolean } = {},
): Promise<StructuredAPIFixture> {
  const basePath = '/api/v1/config/workspaces/' + workspace.workspaceId
  const requests: StructuredAPIRequest[] = []
  const violations: string[] = []
  let previewed = false
  let renamed = false

  await page.route(appOrigin + basePath + '/structured-*', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const headers = request.headers()
    const body = request.postData() === null ? null : parseJSON(request.postData(), violations)
    requests.push({
      method: request.method(),
      path: url.pathname,
      headers,
      body,
    })

    if (url.search !== '' || url.hash !== '') {
      return reject(route, violations, 'structured request included a query or fragment')
    }
    if (!hasSessionCookie(headers.cookie)) {
      return reject(route, violations, 'structured request omitted the HttpOnly session cookie')
    }

    if (url.pathname === basePath + '/structured-config' && request.method() === 'GET') {
      if (body !== null) {
        return reject(route, violations, 'structured catalog GET included a request body')
      }
      return fulfillJSON(route, 200, catalog(workspace, renamed, options.incomplete === true), {
        ETag: workspace.currentDraftETag(),
      })
    }

    if (
      url.pathname === basePath + '/structured-change-previews' &&
      request.method() === 'POST'
    ) {
      if (!requireMutationHeaders(headers, violations, false, workspace.currentDraftETag())) {
        return reject(route, violations, 'structured preview headers were invalid')
      }
      if (!isExpectedRename(body, false)) {
        return reject(route, violations, 'structured preview body was not the exact rename operation')
      }
      previewed = true
      const preview: StructuredChangePreview = {
        preview_id: structuredPreviewID,
        workspace_id: workspace.workspaceId,
        draft_etag: workspace.currentDraftETag(),
        operation_kind: 'upstream.rename',
        target_id: structuredUpstreamID,
        changed_files: [
          {
            path: 'conf.d/upstreams.conf',
            before_digest: 'a'.repeat(64),
            after_digest: 'c'.repeat(64),
            added_lines: 1,
            removed_lines: 1,
            patch:
              '@@ -1 +1 @@\n' +
              '-upstream backend {\n' +
              '+upstream application {\n',
          },
        ],
        complete: true,
      }
      return fulfillJSON(route, 200, preview, { ETag: workspace.currentDraftETag() })
    }

    if (url.pathname === basePath + '/structured-changes' && request.method() === 'POST') {
      if (!previewed) {
        return reject(route, violations, 'structured apply occurred before a matching preview')
      }
      if (!requireMutationHeaders(headers, violations, true, workspace.currentDraftETag())) {
        return reject(route, violations, 'structured apply headers were invalid')
      }
      if (!isExpectedRename(body, true)) {
        return reject(route, violations, 'structured apply body did not bind the exact preview')
      }
      renamed = true
      const nextWorkspace = workspace.commitExternalDraftMutation()
      return fulfillJSON(
        route,
        200,
        {
          workspace: nextWorkspace,
          draft_etag: nextWorkspace.draft_etag,
          changed_paths: ['conf.d/upstreams.conf'],
        },
        { ETag: nextWorkspace.draft_etag },
      )
    }

    return reject(
      route,
      violations,
      'unexpected structured request: ' + request.method() + ' ' + url.pathname,
    )
  })

  return {
    requests: () => requests,
    assertContract: () => {
      expect(violations, 'structured API contract violations').toEqual([])
    },
  }
}

function catalog(
  workspace: WorkspaceAPIFixture,
  renamed: boolean,
  incomplete: boolean,
): StructuredConfig {
  const reference = {
    id: 'd'.repeat(32),
    source: source('conf.d/site.conf', 4),
    state: 'resolved' as const,
    scheme: 'http' as const,
    host: renamed ? 'application' : 'backend',
    port: null,
    uri: '/v1',
    upstream_id: structuredUpstreamID,
    upstream_name: renamed ? 'application' : 'backend',
  }
  return {
    workspace_id: workspace.workspaceId,
    draft_etag: workspace.currentDraftETag(),
    complete: !incomplete,
    project_diagnostics: incomplete
      ? [
          {
            code: 'include_unresolved',
            path: 'nginx.conf',
            line: 3,
            column: 3,
            related_path: 'conf.d/missing.conf',
          },
        ]
      : [],
    http_blocks: [
      {
        id: structuredHTTPBlockID,
        source: source('nginx.conf', 2),
        editable: true,
        instances: 1,
      },
    ],
    upstreams: [
      {
        id: structuredUpstreamID,
        name: renamed ? 'application' : 'backend',
        source: source('conf.d/upstreams.conf', 1),
        servers: [
          {
            id: structuredPeerID,
            source: source('conf.d/upstreams.conf', 2),
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
        preserved_directives: [{ name: 'zone', editable: false }],
        references: [reference],
        editable: true,
        instances: 1,
      },
    ],
    proxy_pass_references: [reference],
    servers: [
      {
        id: structuredServerID,
        source: source('conf.d/site.conf', 1),
        listens: ['80'],
        server_names: ['example.test'],
        summary_truncated: false,
        locations: matcherLocations(reference),
        editable: true,
        instances: 1,
      },
    ],
    diagnostics: [
      {
        domain: 'location',
        code: 'regex_order_sensitive',
        severity: 'warning',
        source: source('conf.d/site.conf', 12),
        related_id: '8'.repeat(32),
        parent_id: structuredServerID,
      },
    ],
  }
}

function matcherLocations(
  reference: StructuredConfig['proxy_pass_references'][number],
): StructuredLocation[] {
  return [
    location('5', 'exact', '/health'),
    location('6', 'prefix', '/api', [reference]),
    location('7', 'prefix_priority', '/assets/'),
    location('8', 'regex', '\\.php$'),
    location('9', 'regex_insensitive', '\\.(gif|jpg)$'),
    location('a', 'named', '@fallback'),
  ]
}

function location(
  id: string,
  type: StructuredLocation['type'],
  matcher: string,
  proxyPasses: StructuredLocation['proxy_passes'] = [],
): StructuredLocation {
  return {
    id: id.repeat(32),
    type,
    matcher,
    source: source('conf.d/site.conf', Number.parseInt(id, 16) + 2),
    children: [],
    proxy_passes: [...proxyPasses],
    unknown_directive_count: type === 'named' ? 1 : 0,
    editable: true,
    proxy_pass_editable: true,
    instances: 1,
  }
}

function source(path: string, line: number): StructuredConfig['http_blocks'][number]['source'] {
  return {
    path,
    start_line: line,
    start_column: 1,
    end_line: line + 1,
    end_column: 2,
  }
}

function requireMutationHeaders(
  headers: Readonly<Record<string, string>>,
  violations: string[],
  requireIfMatch: boolean,
  etag: string,
): boolean {
  let valid = true
  if (headers.origin !== appOrigin) {
    violations.push('structured mutation Origin was ' + String(headers.origin))
    valid = false
  }
  if (headers['content-type'] !== 'application/json') {
    violations.push('structured mutation Content-Type was ' + String(headers['content-type']))
    valid = false
  }
  if (headers['x-csrf-token'] !== csrfToken) {
    violations.push('structured mutation CSRF token was ' + String(headers['x-csrf-token']))
    valid = false
  }
  if (requireIfMatch && headers['if-match'] !== etag) {
    violations.push('structured apply If-Match was ' + String(headers['if-match']))
    valid = false
  }
  if (!requireIfMatch && headers['if-match'] !== undefined) {
    violations.push('structured preview unexpectedly included If-Match')
    valid = false
  }
  return valid
}

function isExpectedRename(value: unknown, apply: boolean): value is StructuredOperation {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return false
  const record = value as Record<string, unknown>
  const expectedKeys = apply ? ['preview_id', 'kind', 'input'] : ['kind', 'input']
  if (
    Object.keys(record).sort().join(',') !== expectedKeys.sort().join(',') ||
    record.kind !== 'upstream.rename' ||
    typeof record.input !== 'object' ||
    record.input === null ||
    Array.isArray(record.input)
  ) {
    return false
  }
  const input = record.input as Record<string, unknown>
  return (
    Object.keys(input).sort().join(',') === 'new_name,upstream_id' &&
    input.upstream_id === structuredUpstreamID &&
    input.new_name === 'application' &&
    (!apply || record.preview_id === structuredPreviewID)
  )
}

function parseJSON(value: string | null, violations: string[]): unknown {
  if (value === null) return null
  try {
    return JSON.parse(value) as unknown
  } catch {
    violations.push('structured request body was not valid JSON')
    return null
  }
}

function hasSessionCookie(cookie: string | undefined): boolean {
  return (
    cookie
      ?.split(';')
      .map((part) => part.trim())
      .includes(sessionCookieName + '=' + sessionCookieValue) === true
  )
}

async function fulfillJSON(
  route: Route,
  status: number,
  body: unknown,
  headers: Readonly<Record<string, string>> = {},
): Promise<void> {
  await route.fulfill({
    status,
    contentType: 'application/json',
    headers: { 'Cache-Control': 'no-store', ...headers },
    body: JSON.stringify(body),
  })
}

async function reject(
  route: Route,
  violations: string[],
  message: string,
): Promise<void> {
  violations.push(message)
  await fulfillJSON(route, 500, {
    error: {
      code: 'E2E_FIXTURE_CONTRACT',
      message,
      request_id: 'structured-e2e',
    },
  })
}
