/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */
import { mount } from '@vue/test-utils'

import StructuredDiagnosticList from './StructuredDiagnosticList.vue'

describe('StructuredDiagnosticList', () => {
  it('communicates severity, code and safe relative source without color alone', () => {
    const wrapper = mount(StructuredDiagnosticList, {
      props: {
        rawEditorPath: '/config/workspaces/workspace-id',
        projectDiagnostics: [
          {
            code: 'include_missing',
            path: 'nginx.conf',
            line: 4,
            column: 3,
            related_path: 'conf.d/missing.conf',
          },
        ],
        diagnostics: [
          {
            domain: 'upstream',
            code: 'upstream_reference_dangling',
            severity: 'warning',
            source: {
              path: 'conf.d/site.conf',
              start_line: 8,
              start_column: 5,
              end_line: 8,
              end_column: 35,
            },
          },
        ],
      },
    })

    expect(wrapper.text()).toContain('Blocking')
    expect(wrapper.text()).toContain('Warning')
    expect(wrapper.text()).toContain('nginx.conf:4:3')
    expect(wrapper.text()).toContain('upstream_reference_dangling')
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    expect(wrapper.get('a').attributes('href')).toBe(
      '/config/workspaces/workspace-id?path=nginx.conf#line-4',
    )
  })
})
