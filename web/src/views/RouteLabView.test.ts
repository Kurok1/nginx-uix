/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick, reactive } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'

import type { RouteAnalysis, RouteTestRun } from '../api/route_lab'
import type { WorkspaceDetail } from '../api/types'
import { appI18n } from '../i18n'
import {
  ROUTE_SIDE_EFFECT_CONFIRMATION,
  type RouteLabState,
  type RouteLabStore,
} from '../route_lab'
import RouteLabView from './RouteLabView.vue'

const workspace: WorkspaceDetail = {
  id: '0123456789abcdef0123456789abcdef',
  name: 'Route draft',
  state: 'ready',
  production_digest: 'a'.repeat(64),
  base_digest: 'a'.repeat(64),
  draft_etag: `"draft-v1:${'b'.repeat(64)}"`,
  entry_count: 2,
  managed_bytes: 128,
  workspace_bytes: 512,
  created_by: 7,
  created_at: '2026-07-21T08:00:00Z',
  updated_at: '2026-07-21T08:01:00Z',
}
const serverRouteID = `srv_${'1'.repeat(32)}`
const predictedLocationID = `loc_${'2'.repeat(32)}`
const observedLocationID = `loc_${'3'.repeat(32)}`
const source = {
  path: 'conf.d/site.conf',
  start_line: 3,
  start_column: 3,
  end_line: 8,
  end_column: 4,
}

const analysis: RouteAnalysis = {
  complete: true,
  normalized_uri: '/api/users',
  predicted_server_route_id: serverRouteID,
  predicted_location_route_id: predictedLocationID,
  runtime_redirect_possible: true,
  servers: [
    {
      route_id: serverRouteID,
      source,
      listeners: [
        {
          address: '',
          port: 80,
          ssl: false,
          default_server: true,
          derived: false,
          supported: true,
        },
      ],
      server_names: ['example.test'],
      disposition: 'selected',
      reason: 'server_name_exact',
    },
  ],
  locations: [
    {
      route_id: predictedLocationID,
      parent_route_id: serverRouteID,
      source,
      matcher_type: 'prefix',
      matcher: '/api',
      depth: 0,
      disposition: 'selected',
      reason: 'location_longest_prefix',
    },
  ],
}

const run: RouteTestRun = {
  id: '11111111111111111111111111111111',
  workspace_id: workspace.id,
  workspace_revision: 2,
  workspace_etag: workspace.draft_etag,
  production_digest: workspace.production_digest,
  draft_digest: 'b'.repeat(64),
  candidate_digest: 'c'.repeat(64),
  state: 'succeeded',
  stage: 'completed',
  safe_request: {
    scheme: 'http',
    host: 'history.test',
    port: 80,
    sni: '',
    method: 'GET',
    uri: '/api/users',
    query: 'page=1',
    headers: [{ name: 'Accept', value: 'application/json' }],
    sensitive_header_names: ['Authorization'],
    body_bytes: 8,
    body_digest: 'd'.repeat(64),
    timeout_ms: 5000,
    assertions: { status_code: 200, contains_text: 'users', forbidden_text: '' },
    side_effecting: true,
    replayable: false,
  },
  static_analysis: analysis,
  terminal_result: {
    agent_result: {
      candidate_digest: 'c'.repeat(64),
      routes: [
        {
          route_id: serverRouteID,
          node_id: '1'.repeat(32),
          parent_route_id: '',
          kind: 'server',
          matcher_type: 'unknown',
          matcher: 'example.test',
          source,
        },
        {
          route_id: observedLocationID,
          node_id: '3'.repeat(32),
          parent_route_id: serverRouteID,
          kind: 'location',
          matcher_type: 'prefix',
          matcher: '/api/users',
          source,
        },
      ],
      response: {
        status_code: 200,
        headers: [{ name: 'Content-Type', value: 'application/json' }],
        body_snippet: '{"users":[]}',
        body_bytes: 12,
        body_digest: 'e'.repeat(64),
        body_truncated: false,
        snippet_omitted: false,
        duration_ms: 18,
        assertions: {
          passed: true,
          complete: true,
          results: [{ kind: 'status_code', passed: true, complete: true }],
        },
      },
      evidence: {
        server_route_id: serverRouteID,
        route_id: observedLocationID,
        final_uri: '/api/users',
        upstream: 'http://backend',
        upstream_status: '200',
        status_code: 200,
        request_time_ms: 16,
      },
      cleanup: { master_reaped: true, port_closed: true, stage_removed: true },
      diagnostics: [],
    },
  },
  replayable: false,
  side_effecting: true,
  body_bytes: 8,
  body_digest: 'd'.repeat(64),
  sensitive_header_names: ['Authorization'],
  created_at: '2026-07-21T08:02:00Z',
  updated_at: '2026-07-21T08:02:01Z',
  started_at: '2026-07-21T08:02:00Z',
  finished_at: '2026-07-21T08:02:01Z',
  stages: [
    {
      sequence: 1,
      stage: 'completed',
      result: 'success',
      details: {},
      occurred_at: '2026-07-21T08:02:01Z',
    },
  ],
}

function storeFixture(): RouteLabStore {
  const state = reactive<RouteLabState>({
    phase: 'ready',
    stream: 'closed',
    analysis,
    analysisWorkspaceId: workspace.id,
    analysisETag: workspace.draft_etag,
    activeRun: run,
    history: [run],
    historyCursor: '',
    historyLoading: false,
    historyWorkspaceId: '',
    error: '',
    historyError: '',
  })
  return {
    state,
    analyze: vi.fn(async () => analysis),
    queue: vi.fn(async () => run),
    resume: vi.fn(async () => run),
    refresh: vi.fn(async () => run),
    cancel: vi.fn(async () => run),
    loadHistory: vi.fn(async () => ({ runs: [run] })),
    clearAnalysis: vi.fn(() => {
      state.analysis = null
    }),
    dispose: vi.fn(),
  }
}

async function mountView(store = storeFixture()) {
  const client = {
    listWorkspaces: vi.fn(async () => [workspace]),
    getWorkspace: vi.fn(async () => workspace),
  }
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/config/route-lab', component: { template: '<div />' } },
      { path: '/config/workspaces/:workspaceId?', component: { template: '<div />' } },
    ],
  })
  await router.push('/config/route-lab')
  await router.isReady()
  const wrapper = mount(RouteLabView, {
    props: { client, store },
    global: { plugins: [router] },
    attachTo: document.body,
  })
  await flushPromises()
  return { client, store, wrapper }
}

describe('RouteLabView', () => {
  beforeEach(() => {
    appI18n.global.locale.value = 'en-US'
  })

  it('renders Route Lab controls in Simplified Chinese', async () => {
    appI18n.global.locale.value = 'zh-CN'
    const { wrapper } = await mountView()

    expect(wrapper.get('h1').text()).toBe('路由实验室')
    expect(wrapper.text()).toContain('仅草稿隔离验证')
    expect(wrapper.text()).toContain('刷新工作区')
    expect(wrapper.text()).toContain('静态分析——仅预测')
    expect(wrapper.text()).toContain('隔离运行结果——未 reload 生产 Nginx')
    wrapper.unmount()
  })

  it('updates every Route Lab work area immediately when the locale changes', async () => {
    const { wrapper } = await mountView()

    appI18n.global.locale.value = 'zh-CN'
    await nextTick()

    expect(wrapper.text()).toContain('请求参数')
    expect(wrapper.text()).toContain('连接语义')
    expect(wrapper.text()).toContain('候选解释')
    expect(wrapper.text()).toContain('Server 候选')
    expect(wrapper.text()).toContain('运行证据')
    expect(wrapper.text()).toContain('预测值')
    expect(wrapper.text()).toContain('观测值')
    expect(wrapper.text()).toContain('已确认沙箱清理')
    expect(wrapper.text()).toContain('路由测试历史')
    expect(wrapper.text()).toContain('查看证据')
    expect(wrapper.get('[aria-label="可滚动的路由测试历史表格"]')).toBeTruthy()
    wrapper.unmount()
  })

  it('keeps static prediction and isolated runtime evidence visibly distinct', async () => {
    const { wrapper } = await mountView()

    expect(wrapper.get('h1').text()).toBe('Route Lab')
    expect(wrapper.text()).toContain('Static analysis — prediction only')
    expect(wrapper.text()).toContain('Isolated runtime result — production Nginx was not reloaded')
    expect(wrapper.text()).toContain('Predicted')
    expect(wrapper.text()).toContain(predictedLocationID)
    expect(wrapper.text()).toContain('Observed')
    expect(wrapper.text()).toContain(observedLocationID)
    expect(wrapper.text()).toContain('Sandbox cleanup confirmed')
    wrapper.unmount()
  })

  it('requires the exact named confirmation before a non-idempotent run', async () => {
    const { store, wrapper } = await mountView()

    await wrapper.get('[name="method"]').setValue('POST')
    await wrapper.get('[data-action="run-route-test"]').trigger('click')
    const modal = wrapper.get('[role="dialog"]')
    expect(modal.text()).toContain('may still reach a configured upstream')
    expect(modal.get('[data-action="confirm"]').attributes('disabled')).toBeDefined()
    await modal.get('input').setValue(ROUTE_SIDE_EFFECT_CONFIRMATION)
    await modal.get('form').trigger('submit')
    await flushPromises()

    expect(store.queue).toHaveBeenCalledWith(
      workspace,
      expect.objectContaining({ method: 'POST' }),
      ROUTE_SIDE_EFFECT_CONFIRMATION,
    )
    wrapper.unmount()
  })

  it('copies only safe history parameters and explains omitted body and secrets', async () => {
    localStorage.clear()
    sessionStorage.clear()
    const { wrapper } = await mountView()

    await wrapper.get('[data-action="use-route-parameters"]').trigger('click')

    expect((wrapper.get('[name="host"]').element as HTMLInputElement).value).toBe('history.test')
    expect((wrapper.get('[name="body"]').element as HTMLTextAreaElement).value).toBe('')
    expect(wrapper.text()).toContain('Body and sensitive headers were not copied')
    expect(localStorage).toHaveLength(0)
    expect(sessionStorage).toHaveLength(0)
    wrapper.unmount()
  })
})
