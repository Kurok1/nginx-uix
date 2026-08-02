/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 1.1.0
 */
import { createAppI18n } from '../i18n'
import { APIClient, APIRequestError } from './client'
import {
  apiRequestID,
  formatAPIRequestError,
  withAPIRequestID,
} from './error_message'

describe('formatAPIRequestError', () => {
  it('maps a stable API code and request ID without exposing the server message', () => {
    const error = new APIRequestError({
      kind: 'api',
      message: 'internal path /private/config',
      status: 401,
      apiError: {
        code: 'invalid_credentials',
        message: 'internal path /private/config',
        request_id: 'request-login',
      },
    })

    expect(formatAPIRequestError(error, createAppI18n('en-US'))).toBe(
      'The username or password is incorrect. Request ID: request-login.',
    )
    expect(formatAPIRequestError(error, createAppI18n('zh-CN'))).toBe(
      '用户名或密码不正确。请求 ID：request-login。',
    )
  })

  it('uses safe localized fallbacks for network and malformed responses', () => {
    expect(formatAPIRequestError(
      new APIRequestError({ kind: 'network', message: 'private network detail' }),
      createAppI18n('zh-CN'),
    )).toBe('无法连接到 Nginx UIX，请检查网络后重试。')
    expect(formatAPIRequestError(
      new APIRequestError({
        kind: 'malformed_response',
        message: 'private response detail',
        requestID: 'request-malformed',
      }),
      createAppI18n('en-US'),
    )).toBe('Nginx UIX returned an invalid response. Request ID: request-malformed.')
  })

  it('uses the unexpected fallback for non-API errors', () => {
    expect(formatAPIRequestError(new Error('secret'), createAppI18n('en-US'))).toBe(
      'Something went wrong. Try again.',
    )
  })

  it('adds request evidence to a view-specific safe fallback without retaining raw details', () => {
    const error = new APIRequestError({
      kind: 'malformed_response',
      message: 'private malformed response detail',
      requestID: 'request-view-fallback',
    })

    expect(apiRequestID(error)).toBe('request-view-fallback')
    expect(apiRequestID(new Error('private detail'))).toBe('')
    expect(withAPIRequestID(
      'The last successful sample is still displayed.',
      error,
      createAppI18n('en-US'),
    )).toBe(
      'The last successful sample is still displayed. Request ID: request-view-fallback.',
    )
  })

  it('keeps a syntactically safe request ID when an unknown response uses the fallback', async () => {
    const client = new APIClient(vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      error: {
        code: 'NEW_SERVER_CODE',
        message: 'server detail must not be shown',
        request_id: 'request-new-code',
      },
    }), {
      status: 500,
      headers: { 'Content-Type': 'application/json' },
    })))

    const error = await client.listWorkspaces().catch((reason: unknown) => reason)

    expect(error).toMatchObject({ kind: 'malformed_response', requestID: 'request-new-code' })
    expect(formatAPIRequestError(error, createAppI18n('en-US'))).toBe(
      'Nginx UIX returned an invalid response. Request ID: request-new-code.',
    )
  })
})
