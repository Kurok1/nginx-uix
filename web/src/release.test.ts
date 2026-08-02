/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
 */
import type { DiffResponse, PublishCheck, Release, WorkspaceDetail } from './api/types'
import { createReleaseStore, type ReleaseClient, type ReleaseEventStream } from './release'
import type { SessionStore } from './session'

const workspace: WorkspaceDetail = {
  id: '0123456789abcdef0123456789abcdef',
  name: 'Production change',
  state: 'ready',
  production_digest: 'a'.repeat(64),
  base_digest: 'a'.repeat(64),
  draft_etag: `"draft-v1:${'b'.repeat(64)}"`,
  entry_count: 2,
  managed_bytes: 128,
  workspace_bytes: 512,
  created_by: 7,
  created_at: '2026-07-18T04:00:00Z',
  updated_at: '2026-07-18T04:01:00Z',
}

const diff: DiffResponse = {
  files: [{ path: 'conf.d/site.conf', status: 'modified', added_lines: 1, removed_lines: 1 }],
  complete: true,
  reason: '',
  patch: 'diff',
}

const check: PublishCheck = {
  id: '11111111111111111111111111111111',
  workspace_id: workspace.id,
  workspace_revision: 2,
  production_digest: workspace.production_digest,
  base_digest: workspace.base_digest,
  draft_digest: 'b'.repeat(64),
  candidate_digest: 'b'.repeat(64),
  manifest_version: 1,
  policy_version: 1,
  validator_version: 1,
  validator_build_id: 'build-id',
  state: 'valid',
  diagnostic_count: 0,
  details: { diagnostics: [] },
  started_at: '2026-07-18T04:02:00Z',
  finished_at: '2026-07-18T04:02:01Z',
  expires_at: '2099-07-18T04:12:01Z',
}

const queued: Release = {
  id: '22222222222222222222222222222222',
  workspace_id: workspace.id,
  check_id: check.id,
  state: 'queued',
  stage: 'queued',
  production_digest: workspace.production_digest,
  draft_digest: check.draft_digest,
  candidate_digest: check.candidate_digest,
  created_at: '2026-07-18T04:03:00Z',
  updated_at: '2026-07-18T04:03:00Z',
  stages: [],
}

class FakeStream implements ReleaseEventStream {
  readonly listeners = new Map<string, Set<EventListener>>()
  closed = false

  addEventListener(type: string, listener: EventListener): void {
    const listeners = this.listeners.get(type) ?? new Set<EventListener>()
    listeners.add(listener)
    this.listeners.set(type, listeners)
  }

  close(): void {
    this.closed = true
  }

  emit(type: string): void {
    for (const listener of this.listeners.get(type) ?? []) listener(new Event(type))
  }
}

function sessionFixture(): SessionStore {
  return {
    state: {
      phase: 'authenticated',
      session: {
        user: { id: 7, username: 'operator', created_at: '2026-07-18T04:00:00Z' },
        csrf_token: 'csrf-token',
        created_at: '2026-07-18T04:00:00Z',
        last_seen_at: '2026-07-18T04:00:00Z',
        idle_expires_at: '2026-07-18T12:00:00Z',
        absolute_expires_at: '2026-07-19T04:00:00Z',
      },
    },
    handleAPIError: () => false,
    login: async () => undefined,
    logout: async () => undefined,
    onExpired: () => () => undefined,
    restore: async () => undefined,
  }
}

function clientFixture(): ReleaseClient & { checks: number; releases: number; reads: number } {
  return {
    checks: 0,
    releases: 0,
    reads: 0,
    async createPublishCheck() {
      this.checks += 1
      return check
    },
    async getPublishCheck() {
      return check
    },
    async createRelease() {
      this.releases += 1
      return queued
    },
    async getRelease() {
      this.reads += 1
      return queued
    },
  }
}

describe('release store', () => {
  it('keeps failures as locale-independent error codes', async () => {
    const client = clientFixture()
    client.createPublishCheck = async () => {
      throw new Error('network failed')
    }
    const store = createReleaseStore(client, sessionFixture(), () => new FakeStream())

    await expect(store.check(workspace, diff, false)).rejects.toThrow('network failed')
    expect(store.state.error).toBe('check_failed')
  })

  it('blocks checks until the complete saved diff has changes', async () => {
    const client = clientFixture()
    const store = createReleaseStore(client, sessionFixture(), () => new FakeStream())

    await expect(store.check(workspace, { ...diff, complete: false }, false)).rejects.toThrow(
      'complete diff',
    )
    await expect(store.check(workspace, diff, true)).rejects.toThrow('unsaved')
    expect(client.checks).toBe(0)

    await expect(store.check(workspace, diff, false)).resolves.toEqual(check)
    expect(store.state.check).toEqual(check)
    expect(client.checks).toBe(1)
  })

  it('queues once, opens the release stream, and refreshes the terminal projection', async () => {
    const client = clientFixture()
    const stream = new FakeStream()
    const store = createReleaseStore(client, sessionFixture(), (url) => {
      expect(url).toBe(`/api/v1/config/releases/${queued.id}/events`)
      return stream
    })
    await store.check(workspace, diff, false)
    const first = store.queue(workspace, workspace.name)
    const second = store.queue(workspace, workspace.name)
    await expect(first).resolves.toEqual(queued)
    await expect(second).resolves.toEqual(queued)
    expect(client.releases).toBe(1)

    const terminal: Release = {
      ...queued,
      backup_id: '33333333333333333333333333333333',
      state: 'succeeded',
      stage: 'committed',
      updated_at: '2026-07-18T04:03:10Z',
      finished_at: '2026-07-18T04:03:10Z',
      stages: [
        {
          sequence: 1,
          stage: 'committed',
          result: 'success',
          details: {},
          occurred_at: '2026-07-18T04:03:10Z',
        },
      ],
    }
    client.getRelease = async () => terminal
    stream.emit('terminal')
    await vi.waitFor(() => expect(store.state.release?.state).toBe('succeeded'))
    expect(stream.closed).toBe(true)
    expect(store.state.stream).toBe('closed')
  })
})
