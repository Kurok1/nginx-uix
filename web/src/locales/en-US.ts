/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 1.1.0
 */
export const enUS = {
  app: {
    title: 'Nginx UIX',
  },
  language: {
    label: 'Language',
    english: 'English',
    simplifiedChinese: '简体中文',
  },
  errors: {
    network: 'Unable to reach Nginx UIX. Check your network and try again.',
    malformedResponse: 'Nginx UIX returned an invalid response.',
    unexpected: 'Something went wrong. Try again.',
    withRequestId: '{message} Request ID: {requestId}.',
    api: {
      invalidCredentials: 'The username or password is incorrect.',
      authenticationRequired: 'Sign in to continue.',
      sessionExpired: 'Your session expired. Sign in again.',
      invalidRequest: 'Check the entered values and try again.',
      requestRejected: 'The request was rejected. Refresh the page and try again.',
      rateLimited: 'Too many requests. Wait a moment and try again.',
      unsupportedMedia: 'The submitted content type is not supported.',
      serviceUnavailable: 'The service is temporarily unavailable. Try again later.',
      internal: 'Nginx UIX could not complete the request.',
      nginxInvalid: 'The Nginx configuration is invalid. Review the diagnostics before continuing.',
      timeout: 'The operation timed out. Check its status before trying again.',
      outputTooLarge: 'The command output exceeded the safe display limit.',
      invalidPath: 'The configuration path is invalid.',
      notManaged: 'This configuration entry is not managed by the workspace.',
      limitExceeded: 'A configured safety limit was exceeded.',
      notFound: 'The requested resource no longer exists.',
      conflict: 'The resource changed. Refresh it before continuing.',
      stale: 'The workspace is stale. Refresh it before editing.',
      needsAttention: 'This operation needs manual attention before it can continue.',
      productionChanged: 'The production configuration changed. Refresh the workspace before publishing.',
      invalidBackup: 'The selected backup cannot be used.',
      agentUnavailable: 'The local Nginx UIX agent is unavailable.',
      noChanges: 'There are no changes to publish.',
      expired: 'This operation plan expired. Create a new plan and try again.',
      inProgress: 'Another operation is already in progress.',
      protected: 'This resource is protected and cannot be changed.',
      unresolved: 'Resolve the outstanding recovery case before continuing.',
      parseFailed: 'The configuration could not be parsed safely.',
      invalidResource: 'The configuration resource is invalid.',
      duplicate: 'A configuration resource with the same identity already exists.',
      referenced: 'The resource is still referenced and cannot be removed.',
      ambiguous: 'The configuration context is ambiguous. Choose a specific target.',
      confirmationRequired: 'Confirm the potentially disruptive operation before continuing.',
      routeUnavailable: 'Route Lab is currently unavailable.',
      busy: 'Route Lab is busy. Wait for the active run to finish.',
      cleanupFailed: 'The operation finished, but automatic cleanup failed.',
      incomplete: 'The operation completed without enough evidence. Review the diagnostics.',
      accountInvalid: 'The ACME account details are invalid.',
      accountDeactivated: 'The ACME account is deactivated.',
      orderFailed: 'The certificate authority could not complete the order.',
      termsRequired: 'Accept the certificate authority terms before continuing.',
      bindingConflict: 'The certificate binding conflicts with the current configuration.',
      fileInvalid: 'The certificate or private-key file is invalid.',
      identifierInvalid: 'One or more certificate identifiers are invalid.',
      keyMismatch: 'The certificate and private key do not match.',
      privateKeyConfirmation: 'Confirm private-key export before continuing.',
      sanMismatch: 'The certificate names do not match the selected server.',
      wildcardRequiresDNS: 'Wildcard certificates require DNS-01 validation.',
      cloudflarePermission: 'The Cloudflare token does not have the required zone permissions.',
      cloudflareToken: 'The Cloudflare token is invalid. Submit a new restricted token.',
      cloudflareUnavailable: 'Cloudflare is temporarily unavailable. Try again later.',
      cloudflareZone: 'The requested Cloudflare zone was not found.',
      dnsTimeout: 'The DNS challenge record did not propagate before the timeout.',
    },
  },
} as const

type StringCatalog<T> = {
  [Key in keyof T]: T[Key] extends string ? string : StringCatalog<T[Key]>
}

export type MessageSchema = StringCatalog<typeof enUS>
