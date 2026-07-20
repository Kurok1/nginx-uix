/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import type { ConfigBackup } from '../api/types'
import BackupInventory from './BackupInventory.vue'

const completeBackup: ConfigBackup = {
  id: '11111111111111111111111111111111',
  origin_type: 'release',
  origin_id: '22222222222222222222222222222222',
  release_id: '22222222222222222222222222222222',
  production_digest: 'a'.repeat(64),
  state: 'complete',
  entry_count: 2,
  total_bytes: 1024,
  body_present: true,
  protected: false,
  manually_protected: false,
  protections: [],
  created_at: '2026-07-19T08:00:00Z',
  verified_at: '2026-07-19T08:00:01Z',
}

describe('BackupInventory', () => {
  it('uses native table and card projections and exposes explicit actions', async () => {
    const wrapper = mount(BackupInventory, {
      props: { backups: [completeBackup], loading: false, nextCursor: '' },
    })

    expect(wrapper.get('table').element.tagName).toBe('TABLE')
    expect(wrapper.get('caption').text()).toContain('immutable recovery points')
    expect(wrapper.get('[data-backup-cards]').findAll('article')).toHaveLength(1)
    await wrapper.get('[data-action="restore"]').trigger('click')
    expect(wrapper.emitted('restore')).toEqual([[completeBackup]])
    await wrapper.get('[data-action="protect"]').trigger('click')
    expect(wrapper.emitted('protect')).toEqual([[completeBackup]])
  })

  it('keeps deleted tombstones readable without mutation controls', () => {
    const wrapper = mount(BackupInventory, {
      props: {
        backups: [{
          ...completeBackup,
          state: 'deleted',
          body_present: false,
          deleted_at: '2026-07-19T10:00:00Z',
        }],
        loading: false,
        nextCursor: '',
      },
    })

    expect(wrapper.text()).toContain('Deleted')
    expect(wrapper.find('[data-action="restore"]').exists()).toBe(false)
    expect(wrapper.find('[data-action="protect"]').exists()).toBe(false)
  })
})
