<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.5.0
-->
<template>
  <section
    class="certificate-page"
    :aria-busy="loading"
  >
    <header class="certificate-page__header">
      <div>
        <p class="certificate-page__eyebrow">
          TLS lifecycle
        </p>
        <h1>Certificates</h1>
        <p>Request, bind, renew, and audit Let's Encrypt certificates without exposing secret material.</p>
      </div>
      <button
        type="button"
        :disabled="loading"
        @click="refreshAll"
      >
        {{ loading ? 'Refreshing…' : 'Refresh evidence' }}
      </button>
    </header>

    <p
      v-if="pageError !== ''"
      class="certificate-page__error"
      role="alert"
    >
      {{ pageError }}
    </p>

    <nav
      class="certificate-page__tabs"
      aria-label="Certificate tasks"
    >
      <button
        v-for="tab in tabs"
        :key="tab.id"
        type="button"
        :aria-pressed="activeTab === tab.id"
        @click="activeTab = tab.id"
      >
        {{ tab.label }}
      </button>
    </nav>

    <section
      class="certificate-page__panel"
      :data-active="activeTab === 'overview'"
      aria-labelledby="certificate-overview-title"
    >
      <h2
        id="certificate-overview-title"
        class="certificate-page__visually-hidden"
      >
        Certificate overview
      </h2>
      <div class="certificate-workbench">
        <aside
          class="certificate-list"
          aria-label="Certificates"
        >
          <p v-if="certificates.length === 0 && !loading">
            No certificates exist. Create an ACME account, then use the Request panel.
          </p>
          <ul v-else>
            <li
              v-for="item in certificates"
              :key="item.id"
            >
              <RouterLink
                :to="`/certificates/${item.id}`"
                :aria-current="selectedCertificate?.id === item.id ? 'page' : undefined"
                @click="openCertificate(item.id)"
              >
                <strong>{{ item.primary_identifier }}</strong>
                <span>{{ item.identifiers.length }} SAN · {{ challengeLabel(item.challenge) }}</span>
                <span>{{ environmentForAccount(item.account_id) }} · {{ stateLabel(item.state) }}</span>
                <span>Expires {{ formatTime(item.not_after) }}</span>
              </RouterLink>
            </li>
          </ul>
        </aside>

        <article
          v-if="selectedCertificate !== null"
          class="certificate-detail"
          aria-labelledby="certificate-detail-title"
        >
          <header>
            <div>
              <p>{{ environmentForAccount(selectedCertificate.account_id) }}</p>
              <h2 id="certificate-detail-title">
                {{ selectedCertificate.primary_identifier }}
              </h2>
            </div>
            <StatusBadge
              :tone="certificateTone(selectedCertificate.state)"
              :label="stateLabel(selectedCertificate.state)"
            />
          </header>
          <dl>
            <div><dt>Certificate ID</dt><dd><code>{{ selectedCertificate.id }}</code></dd></div>
            <div><dt>Challenge</dt><dd>{{ challengeLabel(selectedCertificate.challenge) }}</dd></div>
            <div><dt>Valid from</dt><dd>{{ formatTime(selectedCertificate.not_before) }}</dd></div>
            <div><dt>Valid until</dt><dd>{{ formatTime(selectedCertificate.not_after) }}</dd></div>
            <div><dt>Active version</dt><dd><code>{{ abbreviate(selectedCertificate.active_version_id) }}</code></dd></div>
            <div><dt>Automatic renewal</dt><dd>{{ selectedCertificate.auto_renew ? 'Enabled' : 'Disabled' }}</dd></div>
            <div><dt>Next attempt</dt><dd>{{ optionalTime(selectedCertificate.next_renewal_at) }}</dd></div>
          </dl>
          <section aria-labelledby="certificate-san-title">
            <h3 id="certificate-san-title">
              Subject alternative names
            </h3>
            <ul class="certificate-detail__sans">
              <li
                v-for="identifier in selectedCertificate.identifiers"
                :key="identifier"
              >
                <code>{{ identifier }}</code>
              </li>
            </ul>
          </section>
          <section aria-labelledby="certificate-binding-title">
            <h3 id="certificate-binding-title">
              Server bindings
            </h3>
            <p v-if="(selectedCertificate.bindings?.length ?? 0) === 0">
              Unbound — no Nginx server currently references this certificate.
            </p>
            <ul v-else>
              <li
                v-for="binding in selectedCertificate.bindings"
                :key="binding.id"
              >
                <code>{{ binding.config_path }}</code> · {{ binding.server_names.join(', ') }}
              </li>
            </ul>
          </section>
          <p
            v-if="selectedCertificate.state === 'needs_attention'"
            class="certificate-page__blocking"
            role="alert"
          >
            Certificate or challenge cleanup cannot be confirmed.
          </p>

          <section
            class="certificate-detail__actions"
            aria-labelledby="certificate-lifecycle-title"
          >
            <h3 id="certificate-lifecycle-title">
              Lifecycle controls
            </h3>
            <form
              data-action="renew-certificate"
              @submit.prevent="renewSelectedCertificate"
            >
              <label for="renew-confirmation">Type “{{ selectedCertificate.primary_identifier }}” to renew now</label>
              <input
                id="renew-confirmation"
                v-model="renewConfirmation"
                name="renew-confirmation"
                type="text"
                autocomplete="off"
              >
              <button
                type="submit"
                :disabled="lifecyclePending || renewConfirmation !== selectedCertificate.primary_identifier"
              >
                Renew now
              </button>
            </form>

            <form
              data-action="update-renewal-policy"
              @submit.prevent="saveRenewalPolicy"
            >
              <label class="certificate-detail__check">
                <input
                  v-model="autoRenew"
                  name="auto-renew"
                  type="checkbox"
                >
                Enable automatic renewal
              </label>
              <label for="renew-before-days">Renew before expiry (days)</label>
              <input
                id="renew-before-days"
                v-model.number="renewBeforeDays"
                name="renew-before-days"
                type="number"
                min="1"
                max="89"
              >
              <label for="renewal-policy-confirmation">Type “{{ selectedCertificate.primary_identifier }}” to save this policy</label>
              <input
                id="renewal-policy-confirmation"
                v-model="policyConfirmation"
                name="renewal-policy-confirmation"
                type="text"
                autocomplete="off"
              >
              <button
                type="submit"
                :disabled="lifecyclePending || policyConfirmation !== selectedCertificate.primary_identifier || renewBeforeDays < 1 || renewBeforeDays > 89"
              >
                Save renewal policy
              </button>
            </form>

            <form
              data-action="unbind-certificate"
              @submit.prevent="unbindSelectedCertificate"
            >
              <label for="unbind-confirmation">Type “{{ selectedCertificate.primary_identifier }}” to remove its exact Nginx bindings</label>
              <input
                id="unbind-confirmation"
                v-model="unbindConfirmation"
                name="unbind-confirmation"
                type="text"
                autocomplete="off"
              >
              <button
                type="submit"
                :disabled="lifecyclePending || (selectedCertificate.bindings?.length ?? 0) === 0 || unbindConfirmation !== selectedCertificate.primary_identifier"
              >
                Unbind from Nginx
              </button>
            </form>

            <section
              class="certificate-detail__binding"
              aria-labelledby="standalone-binding-title"
            >
              <h4 id="standalone-binding-title">
                Bind current immutable version
              </h4>
              <p>Selecting servers only prepares a digest-bound configuration review.</p>
              <ul class="certificate-request__servers">
                <li
                  v-for="candidate in serverCandidates"
                  :key="`bind-${candidate.ref.fingerprint}`"
                >
                  <label>
                    <input
                      v-model="bindingServerKeys"
                      type="checkbox"
                      :value="`bind:${candidate.ref.fingerprint}`"
                      :disabled="!candidate.editable"
                      @change="invalidateBindingPlan"
                    >
                    <span>
                      <strong>{{ candidate.ref.server_names.join(', ') || '(no server_name)' }}</strong>
                      <small>{{ candidate.ref.listeners.join(', ') }} · {{ candidate.ref.path }}:{{ candidate.start_line }}</small>
                    </span>
                  </label>
                </li>
              </ul>
              <button
                type="button"
                data-action="review-certificate-binding"
                :disabled="lifecyclePending || bindingServerKeys.length === 0"
                @click="reviewBinding"
              >
                Review binding
              </button>
              <div
                v-if="bindingPlan !== null"
                class="certificate-detail__binding-review"
              >
                <p>No Nginx configuration has been changed by this binding review.</p>
                <p>Plan expires {{ formatTime(bindingPlan.expires_at) }} · production <code>{{ abbreviate(bindingPlan.production_digest) }}</code></p>
                <article
                  v-for="change in bindingPlan.binding_diff"
                  :key="change.path"
                  class="certificate-request__diff"
                >
                  <h4>{{ change.path }} · +{{ change.added_lines }} −{{ change.removed_lines }}</h4>
                  <pre
                    class="workspace-scroll-region"
                    aria-label="Complete standalone binding diff"
                  >{{ change.patch }}</pre>
                </article>
                <label for="binding-confirmation">Type “{{ selectedCertificate.primary_identifier }}” to publish this binding</label>
                <input
                  id="binding-confirmation"
                  v-model="bindingConfirmation"
                  name="binding-confirmation"
                  type="text"
                  autocomplete="off"
                >
                <button
                  type="button"
                  data-action="execute-certificate-binding"
                  :disabled="lifecyclePending || bindingConfirmation !== selectedCertificate.primary_identifier"
                  @click="executeBinding"
                >
                  Bind certificate
                </button>
              </div>
            </section>

            <div class="certificate-detail__utilities">
              <button
                type="button"
                data-action="open-certificate-export"
                :disabled="lifecyclePending"
                @click="openExport($event)"
              >
                Export certificate
              </button>
              <p v-if="(selectedCertificate.bindings?.length ?? 0) > 0">
                Delete is blocked while {{ selectedCertificate.bindings?.length }} Nginx binding remains{{ selectedCertificate.bindings?.length === 1 ? '' : 's' }}.
              </p>
              <label for="delete-confirmation">Type the full certificate ID to delete unreferenced local material</label>
              <input
                id="delete-confirmation"
                v-model="deleteConfirmation"
                name="delete-confirmation"
                type="text"
                autocomplete="off"
              >
              <button
                type="button"
                data-action="delete-certificate"
                :disabled="lifecyclePending || (selectedCertificate.bindings?.length ?? 0) > 0 || deleteConfirmation !== selectedCertificate.id"
                @click="deleteSelectedCertificate"
              >
                Delete certificate
              </button>
            </div>
            <p
              v-if="lifecycleMessage !== ''"
              role="status"
            >
              {{ lifecycleMessage }}
            </p>
            <p
              v-if="lifecycleError !== ''"
              class="certificate-page__error"
              role="alert"
            >
              {{ lifecycleError }}
            </p>
          </section>

          <div
            v-if="exportOpen"
            class="certificate-export__backdrop"
            @click.self="closeExport"
          >
            <section
              ref="exportDialog"
              class="certificate-export"
              role="dialog"
              aria-modal="true"
              aria-labelledby="certificate-export-title"
              @keydown="handleExportKeydown"
            >
              <h3 id="certificate-export-title">
                Export certificate
              </h3>
              <p>The full chain is returned directly to the browser download boundary and is never previewed or retained by this page.</p>
              <label class="certificate-detail__check">
                <input
                  v-model="includePrivateKey"
                  name="include-private-key"
                  type="checkbox"
                >
                Include private key
              </label>
              <p
                v-if="includePrivateKey"
                class="certificate-page__blocking"
              >
                This response contains sensitive private-key material. Store it securely.
              </p>
              <label for="export-confirmation">Type the full certificate ID to export</label>
              <input
                id="export-confirmation"
                v-model="exportConfirmation"
                name="export-confirmation"
                type="text"
                autocomplete="off"
              >
              <template v-if="includePrivateKey">
                <label for="private-key-confirmation">Type “EXPORT PRIVATE KEY” as the second confirmation</label>
                <input
                  id="private-key-confirmation"
                  v-model="privateKeyConfirmation"
                  name="private-key-confirmation"
                  type="text"
                  autocomplete="off"
                >
              </template>
              <div class="certificate-export__actions">
                <button
                  ref="exportCancelButton"
                  type="button"
                  @click="closeExport"
                >
                  Cancel
                </button>
                <button
                  type="button"
                  data-action="export-certificate"
                  :disabled="!canExport || lifecyclePending"
                  @click="exportSelectedCertificate"
                >
                  Export
                </button>
              </div>
            </section>
          </div>
        </article>
      </div>
    </section>

    <section
      class="certificate-page__panel certificate-request"
      :data-active="activeTab === 'request'"
      aria-labelledby="certificate-request-title"
    >
      <header>
        <h2 id="certificate-request-title">
          Request certificate
        </h2>
        <p>Nothing is issued or written to Nginx until a complete review is exactly confirmed.</p>
      </header>
      <ol
        class="certificate-request__steps"
        aria-label="Request steps"
      >
        <li
          v-for="(step, index) in wizardSteps"
          :key="step"
          :aria-current="wizardStep === index + 1 ? 'step' : undefined"
        >
          <span>{{ index + 1 }}</span>{{ step }}
        </li>
      </ol>

      <form @submit.prevent="reviewCertificate">
        <fieldset>
          <legend>1. Identifiers</legend>
          <div class="certificate-request__domains">
            <div
              v-for="(_, index) in identifiers"
              :key="index"
              class="certificate-request__domain"
            >
              <label :for="`certificate-identifier-${index}`">Domain {{ index + 1 }}</label>
              <input
                :id="`certificate-identifier-${index}`"
                v-model="identifiers[index]"
                :name="`identifier-${index}`"
                type="text"
                autocomplete="off"
                spellcheck="false"
                placeholder="example.com"
                @input="invalidatePlan"
              >
              <button
                v-if="identifiers.length > 1"
                type="button"
                :aria-label="`Remove domain ${index + 1}`"
                @click="removeIdentifier(index)"
              >
                Remove
              </button>
            </div>
          </div>
          <button
            type="button"
            @click="addIdentifier"
          >
            Add domain
          </button>
        </fieldset>

        <fieldset>
          <legend>2. Challenge</legend>
          <label for="certificate-challenge">Validation method</label>
          <select
            id="certificate-challenge"
            v-model="challenge"
            name="certificate-challenge"
            @change="invalidatePlan"
          >
            <option value="http_01">
              HTTP-01
            </option>
            <option value="cloudflare_dns_01">
              Cloudflare DNS-01
            </option>
          </select>
          <p
            v-if="challengeError !== ''"
            class="certificate-page__error"
            role="alert"
          >
            {{ challengeError }}
          </p>
        </fieldset>

        <fieldset>
          <legend>3. Account</legend>
          <label for="certificate-account">ACME account</label>
          <select
            id="certificate-account"
            v-model="accountID"
            name="certificate-account"
            @change="invalidatePlan"
          >
            <option value="">
              Select an account
            </option>
            <option
              v-for="account in validAccounts"
              :key="account.id"
              :value="account.id"
            >
              {{ environmentLabel(account.environment) }} · {{ account.email }}
            </option>
          </select>
          <template v-if="selectedEnvironment === 'production'">
            <label for="staging-account">Staging preflight account</label>
            <select
              id="staging-account"
              v-model="stagingAccountID"
              name="staging-account"
              @change="invalidatePlan"
            >
              <option value="">
                No matching staging evidence
              </option>
              <option
                v-for="account in stagingAccounts"
                :key="account.id"
                :value="account.id"
              >
                {{ account.email }}
              </option>
            </select>
          </template>
          <template v-if="challenge === 'cloudflare_dns_01'">
            <label for="dns-credential">Cloudflare Token credential</label>
            <select
              id="dns-credential"
              v-model="dnsCredentialID"
              name="dns-credential"
              @change="invalidatePlan"
            >
              <option value="">
                Select a verified credential
              </option>
              <option
                v-for="item in validCredentials"
                :key="item.id"
                :value="item.id"
              >
                {{ item.name }} · {{ item.fingerprint }}
              </option>
            </select>
          </template>
        </fieldset>

        <fieldset>
          <legend>4. Server bindings</legend>
          <p>Selection only prepares a review; it does not modify Nginx.</p>
          <ul class="certificate-request__servers">
            <li
              v-for="candidate in serverCandidates"
              :key="candidate.ref.fingerprint"
            >
              <label>
                <input
                  v-model="selectedServerFingerprints"
                  type="checkbox"
                  :value="candidate.ref.fingerprint"
                  :disabled="!candidate.editable"
                  @change="invalidatePlan"
                >
                <span>
                  <strong>{{ candidate.ref.server_names.join(', ') || '(no server_name)' }}</strong>
                  <small>{{ candidate.ref.listeners.join(', ') }} · {{ candidate.ref.path }}:{{ candidate.start_line }}</small>
                  <small>{{ candidate.editable ? 'Editable' : candidate.read_only_reason }}</small>
                </span>
              </label>
            </li>
          </ul>
        </fieldset>

        <p
          v-if="wizardError !== '' && wizardError !== challengeError"
          class="certificate-page__error"
          role="alert"
        >
          {{ wizardError }}
        </p>
        <button
          type="button"
          data-action="review-certificate"
          :disabled="wizardPending"
          @click="reviewCertificate"
        >
          {{ wizardPending ? 'Preparing review…' : 'Review certificate request' }}
        </button>
      </form>

      <section
        v-if="orderPlan !== null"
        class="certificate-request__review"
        aria-labelledby="certificate-review-title"
      >
        <h3 id="certificate-review-title">
          5. Review
        </h3>
        <p>No certificate or Nginx configuration has been changed.</p>
        <dl>
          <div><dt>Environment</dt><dd>{{ environmentLabel(orderPlan.environment) }}</dd></div>
          <div><dt>Identifiers</dt><dd>{{ orderPlan.identifiers.join(', ') }}</dd></div>
          <div><dt>Challenge</dt><dd>{{ challengeLabel(orderPlan.challenge) }}</dd></div>
          <div><dt>Servers</dt><dd>{{ orderPlan.server_refs.length }}</dd></div>
          <div><dt>Production identity</dt><dd><code>{{ abbreviate(orderPlan.production_digest) }}</code></dd></div>
          <div><dt>Plan expires</dt><dd>{{ formatTime(orderPlan.expires_at) }}</dd></div>
        </dl>
        <p
          v-if="orderPlan.environment === 'production' && !orderPlan.staging_evidence"
          class="certificate-page__blocking"
        >
          A matching staging preflight is required before production. The explicit production risk phrase is required to proceed without it.
        </p>
        <article
          v-for="change in orderPlan.binding_diff"
          :key="change.path"
          class="certificate-request__diff"
        >
          <h4>{{ change.path }} · +{{ change.added_lines }} −{{ change.removed_lines }}</h4>
          <pre
            class="workspace-scroll-region"
            aria-label="Complete certificate binding diff"
          >{{ change.patch }}</pre>
        </article>

        <section aria-labelledby="certificate-confirm-title">
          <h3 id="certificate-confirm-title">
            6. Confirm
          </h3>
          <p>Issuance may commit certificate files, validate the complete candidate, create a backup, publish, reload, roll back on known failure, and clean up challenge material.</p>
          <label for="certificate-confirmation">Type “{{ orderPlan.primary_identifier }}” exactly to confirm</label>
          <input
            id="certificate-confirmation"
            v-model="confirmation"
            name="certificate-confirmation"
            type="text"
            autocomplete="off"
          >
          <template v-if="orderPlan.requires_risk_confirmation">
            <p class="certificate-page__blocking">
              Production issuance is subject to public CA rate limits.
            </p>
            <label for="production-risk-confirmation">Type “{{ orderPlan.risk_confirmation_phrase }}” to acknowledge the missing staging evidence</label>
            <input
              id="production-risk-confirmation"
              v-model="riskConfirmation"
              name="production-risk-confirmation"
              type="text"
              autocomplete="off"
            >
          </template>
          <button
            type="button"
            data-action="execute-certificate-plan"
            :disabled="!canExecutePlan || wizardPending"
            @click="executePlan"
          >
            {{ wizardPending ? 'Queueing…' : 'Issue certificate' }}
          </button>
        </section>
      </section>
    </section>

    <section
      class="certificate-page__panel certificate-accounts"
      :data-active="activeTab === 'accounts'"
      aria-labelledby="certificate-accounts-title"
    >
      <header>
        <h2 id="certificate-accounts-title">
          Accounts &amp; DNS credentials
        </h2>
        <p>Read responses contain metadata only. Account keys and Tokens cannot be retrieved.</p>
      </header>
      <div class="certificate-accounts__grid">
        <section aria-labelledby="acme-accounts-title">
          <h3 id="acme-accounts-title">
            ACME accounts
          </h3>
          <ul>
            <li
              v-for="account in accounts"
              :key="account.id"
            >
              <strong>{{ environmentLabel(account.environment) }}</strong> · {{ account.email }}
              <span>{{ account.status }} · <code>{{ abbreviate(account.id) }}</code></span>
              <a
                :href="account.terms_url"
                target="_blank"
                rel="noreferrer"
              >Current Terms</a>
              <button
                v-if="account.status === 'valid'"
                type="button"
                data-action="deactivate-account"
                :data-id="account.id"
                @click="openAccountDeactivation(account, $event)"
              >
                Deactivate account
              </button>
            </li>
          </ul>
          <p v-if="accounts.length === 0">
            Create an ACME account before requesting a certificate.
          </p>
          <form
            data-action="create-acme-account"
            @submit.prevent="createAccount"
          >
            <h4>Create account</h4>
            <label for="account-environment">Environment</label>
            <select
              id="account-environment"
              v-model="accountEnvironment"
              name="account-environment"
            >
              <option value="staging">
                Staging
              </option>
              <option value="production">
                Production
              </option>
            </select>
            <label for="account-email">Contact email</label>
            <input
              id="account-email"
              v-model="accountEmail"
              name="account-email"
              type="email"
              autocomplete="email"
            >
            <label class="certificate-detail__check">
              <input
                v-model="accountTermsAccepted"
                name="account-terms"
                type="checkbox"
              >
              <span>
                I agree to the
                <a
                  :href="termsURL(accountEnvironment)"
                  target="_blank"
                  rel="noreferrer"
                >current Terms of Service</a>
              </span>
            </label>
            <button
              type="submit"
              :disabled="accountPending || accountEmail.trim() === '' || !accountTermsAccepted"
            >
              {{ accountPending ? 'Creating…' : 'Create ACME account' }}
            </button>
          </form>

          <form
            data-action="import-acme-account"
            @submit.prevent="importAccount"
          >
            <h4>Import existing account</h4>
            <label for="import-environment">Environment</label>
            <select
              id="import-environment"
              v-model="importEnvironment"
              name="import-environment"
            >
              <option value="staging">
                Staging
              </option>
              <option value="production">
                Production
              </option>
            </select>
            <label for="import-email">Contact email</label>
            <input
              id="import-email"
              v-model="importEmail"
              name="import-email"
              type="email"
              autocomplete="email"
            >
            <label for="import-account-uri">Exact account URI</label>
            <input
              id="import-account-uri"
              v-model="importAccountURI"
              name="import-account-uri"
              type="url"
              autocomplete="off"
              spellcheck="false"
            >
            <label for="import-private-key">Account private key PEM</label>
            <textarea
              id="import-private-key"
              v-model="importPrivateKey"
              name="import-private-key"
              autocomplete="new-password"
              spellcheck="false"
            />
            <label class="certificate-detail__check">
              <input
                v-model="importTermsAccepted"
                name="import-terms"
                type="checkbox"
              >
              <span>
                I agree to the
                <a
                  :href="termsURL(importEnvironment)"
                  target="_blank"
                  rel="noreferrer"
                >current Terms of Service</a>
              </span>
            </label>
            <button
              type="submit"
              :disabled="accountPending || importEmail.trim() === '' || importAccountURI.trim() === '' || importPrivateKey === '' || !importTermsAccepted"
            >
              {{ accountPending ? 'Importing…' : 'Import ACME account' }}
            </button>
          </form>
          <p
            v-if="accountMessage !== ''"
            role="status"
          >
            {{ accountMessage }}
          </p>
          <p
            v-if="accountError !== ''"
            class="certificate-page__error"
            role="alert"
          >
            {{ accountError }}
          </p>
        </section>

        <section aria-labelledby="cloudflare-credentials-title">
          <h3 id="cloudflare-credentials-title">
            Cloudflare API Token
          </h3>
          <p class="certificate-accounts__secret-help">
            Grant only <strong>Zone Read</strong> and <strong>DNS Write</strong>, and restrict resources to the required Zone. Global API Key is unsupported.
          </p>
          <form
            data-action="save-cloudflare-token"
            @submit.prevent="saveCloudflareCredential"
          >
            <label for="credential-name">Credential name</label>
            <input
              id="credential-name"
              v-model="credentialName"
              name="credential-name"
              type="text"
              autocomplete="off"
              maxlength="128"
            >
            <label for="cloudflare-token">Cloudflare API Token</label>
            <input
              id="cloudflare-token"
              v-model="cloudflareToken"
              name="cloudflare-token"
              type="password"
              autocomplete="new-password"
              spellcheck="false"
            >
            <button
              type="submit"
              :disabled="credentialPending || credentialName.trim() === '' || cloudflareToken === ''"
            >
              {{ credentialPending ? 'Verifying…' : 'Verify and save Token' }}
            </button>
          </form>
          <p
            v-if="credentialMessage !== ''"
            role="status"
          >
            {{ credentialMessage }}
          </p>
          <p
            v-if="credentialError !== ''"
            class="certificate-page__error"
            role="alert"
          >
            {{ credentialError }}
          </p>
          <ul class="certificate-accounts__credentials">
            <li
              v-for="item in credentials"
              :key="item.id"
            >
              <strong>{{ item.name }}</strong>
              <span>{{ item.status }} · fingerprint <code>{{ item.fingerprint }}</code></span>
              <span>Verified {{ formatTime(item.verified_at) }}</span>
              <button
                v-if="item.status !== 'deleted'"
                type="button"
                data-action="delete-dns-credential"
                :data-id="item.id"
                @click="openCredentialDeletion(item, $event)"
              >
                Delete credential
              </button>
            </li>
          </ul>
        </section>
      </div>
    </section>

    <section
      class="certificate-page__panel certificate-history"
      :data-active="activeTab === 'history'"
      aria-labelledby="certificate-history-title"
    >
      <header>
        <h2 id="certificate-history-title">
          Task history
        </h2>
        <p>Leaving this page does not cancel the task. Cancellation is always an explicit server operation.</p>
      </header>
      <div class="certificate-history__layout">
        <ul class="certificate-history__tasks">
          <li
            v-for="task in tasks"
            :key="task.id"
          >
            <button
              type="button"
              :aria-pressed="selectedTask?.id === task.id"
              @click="openTask(task.id)"
            >
              <strong>{{ task.kind }} · {{ task.state }}</strong>
              <span><code>{{ abbreviate(task.id) }}</code> · {{ formatTime(task.created_at) }}</span>
            </button>
          </li>
        </ul>
        <article
          v-if="selectedTask !== null"
          class="certificate-history__detail"
          aria-labelledby="certificate-task-title"
        >
          <header>
            <div>
              <h3 id="certificate-task-title">
                {{ selectedTask.kind }} task
              </h3>
              <code>{{ selectedTask.id }}</code>
            </div>
            <StatusBadge
              :tone="taskTone(selectedTask.state)"
              :label="selectedTask.state"
            />
          </header>
          <p
            aria-live="polite"
            aria-atomic="true"
          >
            {{ currentTaskPhrase }}
          </p>
          <p>Stream: {{ streamLabel }}</p>
          <p
            v-if="selectedTask.state === 'needs_attention'"
            class="certificate-page__blocking"
            role="alert"
          >
            Certificate or challenge cleanup cannot be confirmed.
          </p>
          <ol class="certificate-history__timeline">
            <li
              v-for="stage in selectedTask.stages"
              :key="stage.sequence"
            >
              <span aria-hidden="true">◇</span>
              <div><strong>{{ stageGroup(stage.stage) }} · {{ stage.stage }}</strong><span>{{ stage.result }} · {{ formatTime(stage.occurred_at) }}</span></div>
            </li>
          </ol>
          <button
            v-if="!isTerminalCertificateTask(selectedTask.state)"
            type="button"
            :disabled="taskPending"
            @click="cancelTask"
          >
            {{ selectedTask.state === 'cancelling' ? 'Cancelling…' : 'Cancel task' }}
          </button>
        </article>
      </div>
    </section>

    <OperationConfirmModal
      :open="accountDeactivateTarget !== null"
      title="Deactivate ACME account?"
      consequence="Existing certificates remain served, but renewals using this account stop. Remote and local state must both confirm deactivation."
      :confirmation-text="accountDeactivateTarget?.id ?? ''"
      confirm-label="Deactivate account"
      :requires-reason="false"
      :pending="accountPending"
      :trigger="accountModalTrigger"
      @cancel="closeAccountDeactivation"
      @confirm="deactivateAccount"
    />
    <OperationConfirmModal
      :open="credentialDeleteTarget !== null"
      title="Delete Cloudflare credential?"
      consequence="Future DNS-01 tasks cannot use this credential. Active tasks and referenced renewal policies block deletion on the server."
      :confirmation-text="credentialDeleteTarget?.id ?? ''"
      confirm-label="Delete credential"
      :requires-reason="false"
      :pending="credentialPending"
      :trigger="credentialModalTrigger"
      @cancel="closeCredentialDeletion"
      @confirm="deleteCredential"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'

import {
  certificateTaskEventsPath,
  isTerminalCertificateTask,
  parseCertificateTaskStageEvent,
  type ACMEAccount,
  type ACMEDirectory,
  type CertificateChallenge,
  type CertificateBindingPlan,
  type CertificateEnvironment,
  type CertificateOrderPlan,
  type CertificateRecord,
  type CertificateServerCandidate,
  type CertificateState,
  type CertificateTask,
  type CertificateTaskStageName,
  type DNSCredential,
} from '../api/certificates'
import { APIRequestError, apiClient, type APIClient } from '../api/client'
import OperationConfirmModal from '../components/OperationConfirmModal.vue'
import StatusBadge, { type StatusTone } from '../components/StatusBadge.vue'
import { useFocusTrap } from '../composables/useFocusTrap'
import { sessionStore } from '../session'

type CertificateViewClient = Pick<
  APIClient,
  | 'listACMEDirectories'
  | 'listACMEAccounts'
  | 'createACMEAccount'
  | 'importACMEAccount'
  | 'deactivateACMEAccount'
  | 'listCertificateDNSCredentials'
  | 'createCertificateDNSCredential'
  | 'deleteCertificateDNSCredential'
  | 'listCertificateServerCandidates'
  | 'createCertificateOrderPlan'
  | 'executeCertificateOrderPlan'
  | 'listCertificateTasks'
  | 'getCertificateTask'
  | 'cancelCertificateTask'
  | 'listCertificates'
  | 'getCertificate'
  | 'renewCertificate'
  | 'updateCertificateRenewalPolicy'
  | 'createCertificateBindingPlan'
  | 'executeCertificateBindingPlan'
  | 'unbindCertificate'
  | 'exportCertificate'
  | 'deleteCertificate'
>

interface EventSourceLike {
  onopen: ((event: Event) => void) | null
  onmessage: ((event: MessageEvent<string>) => void) | null
  onerror: ((event: Event) => void) | null
  close: () => void
}

const props = withDefaults(defineProps<{
  certificateId?: string
  client?: CertificateViewClient
  csrfToken?: string
  eventSourceFactory?: (path: string) => EventSourceLike
  saveFile?: (file: { blob: Blob; filename: string }) => void
}>(), {
  certificateId: '',
  client: () => apiClient,
  csrfToken: '',
  eventSourceFactory: (path: string) => new EventSource(path),
  saveFile: () => undefined,
})
const csrfToken = computed(
  () => props.csrfToken || sessionStore.state.session?.csrf_token || '',
)

type Tab = 'overview' | 'request' | 'accounts' | 'history'
const tabs: ReadonlyArray<{ id: Tab; label: string }> = [
  { id: 'overview', label: 'Overview' },
  { id: 'request', label: 'Request' },
  { id: 'accounts', label: 'Accounts' },
  { id: 'history', label: 'History' },
]
const wizardSteps = ['Identifiers', 'Challenge', 'Account', 'Server bindings', 'Review', 'Confirm'] as const

const activeTab = ref<Tab>('overview')
const loading = ref(true)
const pageError = ref('')
const directories = ref<ACMEDirectory[]>([])
const accounts = ref<ACMEAccount[]>([])
const credentials = ref<DNSCredential[]>([])
const serverCandidates = ref<CertificateServerCandidate[]>([])
const certificates = ref<CertificateRecord[]>([])
const selectedCertificate = ref<CertificateRecord | null>(null)
const tasks = ref<CertificateTask[]>([])
const selectedTask = ref<CertificateTask | null>(null)

const identifiers = ref([''])
const challenge = ref<CertificateChallenge>('http_01')
const accountID = ref('')
const stagingAccountID = ref('')
const dnsCredentialID = ref('')
const selectedServerFingerprints = ref<string[]>([])
const orderPlan = ref<CertificateOrderPlan | null>(null)
const confirmation = ref('')
const riskConfirmation = ref('')
const wizardStep = ref(1)
const wizardError = ref('')
const wizardPending = ref(false)

const credentialName = ref('')
const cloudflareToken = ref('')
const credentialPending = ref(false)
const credentialError = ref('')
const credentialMessage = ref('')

const accountEnvironment = ref<CertificateEnvironment>('staging')
const accountEmail = ref('')
const accountTermsAccepted = ref(false)
const importEnvironment = ref<CertificateEnvironment>('staging')
const importEmail = ref('')
const importAccountURI = ref('')
const importPrivateKey = ref('')
const importTermsAccepted = ref(false)
const accountPending = ref(false)
const accountError = ref('')
const accountMessage = ref('')
const accountDeactivateTarget = ref<ACMEAccount | null>(null)
const accountModalTrigger = ref<HTMLElement | null>(null)
const credentialDeleteTarget = ref<DNSCredential | null>(null)
const credentialModalTrigger = ref<HTMLElement | null>(null)

const renewConfirmation = ref('')
const autoRenew = ref(true)
const renewBeforeDays = ref(30)
const policyConfirmation = ref('')
const unbindConfirmation = ref('')
const deleteConfirmation = ref('')
const lifecyclePending = ref(false)
const lifecycleError = ref('')
const lifecycleMessage = ref('')
const exportOpen = ref(false)
const exportConfirmation = ref('')
const includePrivateKey = ref(false)
const privateKeyConfirmation = ref('')
const exportDialog = ref<HTMLElement | null>(null)
const exportCancelButton = ref<HTMLButtonElement | null>(null)
const exportTrigger = ref<HTMLElement | null>(null)
const bindingServerKeys = ref<string[]>([])
const bindingPlan = ref<CertificateBindingPlan | null>(null)
const bindingConfirmation = ref('')

const streamState = ref<'closed' | 'connecting' | 'connected' | 'reconnecting'>('closed')
const taskPending = ref(false)
let taskSource: EventSourceLike | null = null
let streamedTaskID = ''
const exportTrap = useFocusTrap(exportDialog, exportTrigger)

const validAccounts = computed(() => accounts.value.filter((account) => account.status === 'valid'))
const stagingAccounts = computed(() => validAccounts.value.filter((account) => account.environment === 'staging'))
const validCredentials = computed(() => credentials.value.filter((item) => item.status === 'valid'))
const selectedEnvironment = computed<CertificateEnvironment | ''>(() =>
  validAccounts.value.find((account) => account.id === accountID.value)?.environment ?? '',
)
const normalizedIdentifiers = computed(() => identifiers.value.map((item) => item.trim().toLowerCase()).filter(Boolean))
const hasWildcard = computed(() => normalizedIdentifiers.value.some((item) => item.startsWith('*.')))
const challengeError = computed(() =>
  hasWildcard.value && challenge.value === 'http_01'
    ? 'Wildcard certificates require Cloudflare DNS-01'
    : '',
)
const canExecutePlan = computed(() => {
  const plan = orderPlan.value
  if (plan === null || plan.state !== 'planned' || confirmation.value !== plan.primary_identifier) return false
  return !plan.requires_risk_confirmation || riskConfirmation.value === plan.risk_confirmation_phrase
})
const canExport = computed(() => {
  const item = selectedCertificate.value
  if (item === null || exportConfirmation.value !== item.id) return false
  return !includePrivateKey.value || privateKeyConfirmation.value === 'EXPORT PRIVATE KEY'
})
const currentTaskPhrase = computed(() => selectedTask.value === null ? '' : `${selectedTask.value.stage} — ${selectedTask.value.state}`)
const streamLabel = computed(() => ({
  closed: 'Closed',
  connecting: 'Connecting',
  connected: 'Connected',
  reconnecting: 'Reconnecting — the server task continues',
})[streamState.value])

onMounted(() => void refreshAll())
onBeforeUnmount(closeTaskStream)
watch(() => props.certificateId, (id) => {
  if (id !== '') void openCertificate(id)
})

async function refreshAll(): Promise<void> {
  loading.value = true
  pageError.value = ''
  try {
    const [loadedDirectories, loadedAccounts, loadedCredentials, loadedCandidates, loadedCertificates, loadedTasks] = await Promise.all([
      props.client.listACMEDirectories(),
      props.client.listACMEAccounts(),
      props.client.listCertificateDNSCredentials(),
      props.client.listCertificateServerCandidates(),
      props.client.listCertificates(100),
      props.client.listCertificateTasks(100),
    ])
    directories.value = loadedDirectories
    accounts.value = loadedAccounts
    credentials.value = loadedCredentials
    serverCandidates.value = loadedCandidates
    certificates.value = loadedCertificates
    tasks.value = loadedTasks
    setAccountDefaults()
    const preferredID = props.certificateId || selectedCertificate.value?.id || loadedCertificates[0]?.id || ''
    if (preferredID !== '') await openCertificate(preferredID)
    const active = loadedTasks.find((task) => !isTerminalCertificateTask(task.state)) ?? loadedTasks[0]
    if (active !== undefined) {
      selectedTask.value = active
      if (!isTerminalCertificateTask(active.state)) startTaskStream(active.id)
    }
  } catch (error) {
    pageError.value = safeMessage(error, 'Certificate evidence could not be loaded.')
  } finally {
    loading.value = false
  }
}

async function openCertificate(id: string): Promise<void> {
  try {
    const item = await props.client.getCertificate(id)
    selectedCertificate.value = item
    autoRenew.value = item.auto_renew
    renewBeforeDays.value = Math.max(1, Math.round(item.renew_before_seconds / 86_400))
    clearLifecycleConfirmations()
  } catch (error) {
    pageError.value = safeMessage(error, 'Certificate detail could not be loaded.')
  }
}

function setAccountDefaults(): void {
  if (!validAccounts.value.some((account) => account.id === accountID.value)) {
    accountID.value = validAccounts.value[0]?.id ?? ''
  }
  if (!stagingAccounts.value.some((account) => account.id === stagingAccountID.value)) {
    stagingAccountID.value = stagingAccounts.value[0]?.id ?? ''
  }
  if (!validCredentials.value.some((item) => item.id === dnsCredentialID.value)) {
    dnsCredentialID.value = validCredentials.value[0]?.id ?? ''
  }
}

function addIdentifier(): void {
  if (identifiers.value.length < 100) identifiers.value.push('')
  invalidatePlan()
}

function removeIdentifier(index: number): void {
  identifiers.value.splice(index, 1)
  invalidatePlan()
}

function invalidatePlan(): void {
  orderPlan.value = null
  confirmation.value = ''
  riskConfirmation.value = ''
  wizardError.value = ''
  wizardStep.value = 1
}

async function reviewCertificate(): Promise<void> {
  wizardError.value = validateWizard()
  if (wizardError.value !== '') return
  wizardPending.value = true
  try {
    const refs = serverCandidates.value
      .filter((candidate) => selectedServerFingerprints.value.includes(candidate.ref.fingerprint))
      .map((candidate) => candidate.ref)
    orderPlan.value = await props.client.createCertificateOrderPlan({
      identifiers: normalizedIdentifiers.value,
      challenge: challenge.value,
      account_id: accountID.value,
      ...(selectedEnvironment.value === 'production' && stagingAccountID.value !== '' ? { staging_account_id: stagingAccountID.value } : {}),
      ...(challenge.value === 'cloudflare_dns_01' ? { dns_credential_id: dnsCredentialID.value } : {}),
      server_refs: refs,
    }, csrfToken.value)
    confirmation.value = ''
    riskConfirmation.value = ''
    wizardStep.value = 5
  } catch (error) {
    wizardError.value = safeMessage(error, 'The certificate review could not be prepared.')
  } finally {
    wizardPending.value = false
  }
}

function validateWizard(): string {
  if (normalizedIdentifiers.value.length === 0) return 'At least one domain is required.'
  if (new Set(normalizedIdentifiers.value).size !== normalizedIdentifiers.value.length) return 'Duplicate domains are not allowed.'
  if (normalizedIdentifiers.value.some((identifier) => !validIdentifier(identifier))) return 'One or more domains are invalid.'
  if (challengeError.value !== '') return challengeError.value
  if (accountID.value === '') return 'Select a valid ACME account.'
  if (challenge.value === 'cloudflare_dns_01' && dnsCredentialID.value === '') return 'Select a verified Cloudflare Token credential.'
  if (selectedServerFingerprints.value.length === 0) return 'Select at least one editable Nginx server.'
  return ''
}

function validIdentifier(value: string): boolean {
  const domain = value.startsWith('*.') ? value.slice(2) : value
  if (domain.length < 1 || domain.length > 253 || value.includes('*') && !value.startsWith('*.')) return false
  const labels = domain.split('.')
  return labels.length >= 2 && labels.every((label) => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label))
}

async function executePlan(): Promise<void> {
  const plan = orderPlan.value
  if (plan === null || !canExecutePlan.value) return
  wizardPending.value = true
  try {
    const task = await props.client.executeCertificateOrderPlan(plan.id, {
      confirmation: confirmation.value,
      production_risk_confirmation: riskConfirmation.value,
    }, csrfToken.value)
    tasks.value = [task, ...tasks.value.filter((item) => item.id !== task.id)]
    selectedTask.value = task
    confirmation.value = ''
    riskConfirmation.value = ''
    activeTab.value = 'history'
    startTaskStream(task.id)
  } catch (error) {
    wizardError.value = safeMessage(error, 'The certificate task could not be queued.')
  } finally {
    wizardPending.value = false
  }
}

async function saveCloudflareCredential(): Promise<void> {
  const name = credentialName.value.trim()
  const token = cloudflareToken.value
  if (name === '' || token === '') return
  credentialPending.value = true
  credentialError.value = ''
  credentialMessage.value = ''
  try {
    const item = await props.client.createCertificateDNSCredential({ name, api_token: token }, csrfToken.value)
    cloudflareToken.value = ''
    credentialName.value = ''
    credentials.value = [item, ...credentials.value.filter((existing) => existing.id !== item.id)]
    dnsCredentialID.value = item.id
    credentialMessage.value = 'Cloudflare Token verified and saved. The submitted Token is no longer retained by this page.'
  } catch (error) {
    credentialError.value = safeMessage(error, 'The Cloudflare Token could not be verified.')
  } finally {
    credentialPending.value = false
  }
}

async function createAccount(): Promise<void> {
  const email = accountEmail.value.trim()
  if (email === '' || !accountTermsAccepted.value) return
  accountPending.value = true
  accountError.value = ''
  accountMessage.value = ''
  try {
    const account = await props.client.createACMEAccount({
      environment: accountEnvironment.value,
      email,
      terms_of_service_agreed: true,
    }, csrfToken.value)
    accounts.value = [account, ...accounts.value.filter((item) => item.id !== account.id)]
    accountEmail.value = ''
    accountTermsAccepted.value = false
    accountMessage.value = `${environmentLabel(account.environment)} ACME account created.`
    setAccountDefaults()
  } catch (error) {
    accountError.value = safeMessage(error, 'The ACME account could not be created.')
  } finally {
    accountPending.value = false
  }
}

async function importAccount(): Promise<void> {
  const email = importEmail.value.trim()
  const accountURI = importAccountURI.value.trim()
  const privateKey = importPrivateKey.value
  if (email === '' || accountURI === '' || privateKey === '' || !importTermsAccepted.value) return
  accountPending.value = true
  accountError.value = ''
  accountMessage.value = ''
  try {
    const account = await props.client.importACMEAccount({
      environment: importEnvironment.value,
      email,
      account_uri: accountURI,
      private_key_pem: privateKey,
      terms_of_service_agreed: true,
    }, csrfToken.value)
    importPrivateKey.value = ''
    importEmail.value = ''
    importAccountURI.value = ''
    importTermsAccepted.value = false
    accounts.value = [account, ...accounts.value.filter((item) => item.id !== account.id)]
    accountMessage.value = `${environmentLabel(account.environment)} ACME account imported. The private key is no longer retained by this page.`
    setAccountDefaults()
  } catch (error) {
    accountError.value = safeMessage(error, 'The ACME account could not be imported.')
  } finally {
    accountPending.value = false
  }
}

function openAccountDeactivation(account: ACMEAccount, event: Event): void {
  accountModalTrigger.value = event.currentTarget instanceof HTMLElement ? event.currentTarget : null
  accountDeactivateTarget.value = account
  accountError.value = ''
}

function closeAccountDeactivation(): void {
  accountDeactivateTarget.value = null
}

async function deactivateAccount(_reason: string, confirmation: string): Promise<void> {
  const account = accountDeactivateTarget.value
  if (account === null || confirmation !== account.id) return
  accountPending.value = true
  accountError.value = ''
  accountMessage.value = ''
  try {
    const updated = await props.client.deactivateACMEAccount(account.id, csrfToken.value)
    accounts.value = accounts.value.map((item) => item.id === updated.id ? updated : item)
    closeAccountDeactivation()
    accountMessage.value = 'ACME account deactivated. Existing certificate files remain unchanged.'
    setAccountDefaults()
  } catch (error) {
    accountError.value = safeMessage(error, 'The ACME account could not be deactivated safely.')
  } finally {
    accountPending.value = false
  }
}

function openCredentialDeletion(item: DNSCredential, event: Event): void {
  credentialModalTrigger.value = event.currentTarget instanceof HTMLElement ? event.currentTarget : null
  credentialDeleteTarget.value = item
  credentialError.value = ''
}

function closeCredentialDeletion(): void {
  credentialDeleteTarget.value = null
}

async function deleteCredential(_reason: string, confirmation: string): Promise<void> {
  const item = credentialDeleteTarget.value
  if (item === null || confirmation !== item.id) return
  credentialPending.value = true
  credentialError.value = ''
  credentialMessage.value = ''
  try {
    await props.client.deleteCertificateDNSCredential(item.id, csrfToken.value)
    credentials.value = credentials.value.filter((credential) => credential.id !== item.id)
    closeCredentialDeletion()
    credentialMessage.value = 'Cloudflare credential deleted; its Token was never returned to the page.'
    setAccountDefaults()
  } catch (error) {
    credentialError.value = safeMessage(error, 'The Cloudflare credential could not be deleted.')
  } finally {
    credentialPending.value = false
  }
}

function termsURL(environment: CertificateEnvironment): string {
  return directories.value.find((directory) => directory.environment === environment)?.terms_url ?? '#'
}

async function renewSelectedCertificate(): Promise<void> {
  const item = selectedCertificate.value
  if (item === null || renewConfirmation.value !== item.primary_identifier) return
  lifecyclePending.value = true
  clearLifecycleResult()
  try {
    const task = await props.client.renewCertificate(item.id, renewConfirmation.value, csrfToken.value)
    renewConfirmation.value = ''
    replaceTask(task)
    selectedTask.value = task
    lifecycleMessage.value = 'Renewal task queued. Leaving this page will not cancel it.'
    startTaskStream(task.id)
  } catch (error) {
    lifecycleError.value = safeMessage(error, 'The renewal task could not be queued.')
  } finally {
    lifecyclePending.value = false
  }
}

async function saveRenewalPolicy(): Promise<void> {
  const item = selectedCertificate.value
  if (item === null || policyConfirmation.value !== item.primary_identifier || renewBeforeDays.value < 1 || renewBeforeDays.value > 89) return
  lifecyclePending.value = true
  clearLifecycleResult()
  try {
    const updated = await props.client.updateCertificateRenewalPolicy(item.id, {
      confirmation: policyConfirmation.value,
      auto_renew: autoRenew.value,
      renew_before_seconds: renewBeforeDays.value * 86_400,
    }, csrfToken.value)
    replaceCertificate(updated)
    policyConfirmation.value = ''
    lifecycleMessage.value = 'Automatic-renewal policy updated.'
  } catch (error) {
    lifecycleError.value = safeMessage(error, 'The renewal policy could not be updated.')
  } finally {
    lifecyclePending.value = false
  }
}

async function unbindSelectedCertificate(): Promise<void> {
  const item = selectedCertificate.value
  if (item === null || unbindConfirmation.value !== item.primary_identifier) return
  lifecyclePending.value = true
  clearLifecycleResult()
  try {
    const updated = await props.client.unbindCertificate(item.id, unbindConfirmation.value, csrfToken.value)
    replaceCertificate(updated)
    unbindConfirmation.value = ''
    lifecycleMessage.value = 'Exact certificate bindings were removed after validated publication.'
  } catch (error) {
    lifecycleError.value = safeMessage(error, 'The certificate could not be unbound.')
  } finally {
    lifecyclePending.value = false
  }
}

function invalidateBindingPlan(): void {
  bindingPlan.value = null
  bindingConfirmation.value = ''
}

async function reviewBinding(): Promise<void> {
  const item = selectedCertificate.value
  if (item === null || bindingServerKeys.value.length === 0) return
  lifecyclePending.value = true
  clearLifecycleResult()
  try {
    const fingerprints = new Set(bindingServerKeys.value.map((key) => key.replace(/^bind:/, '')))
    const refs = serverCandidates.value
      .filter((candidate) => fingerprints.has(candidate.ref.fingerprint))
      .map((candidate) => candidate.ref)
    bindingPlan.value = await props.client.createCertificateBindingPlan(item.id, refs, csrfToken.value)
    bindingConfirmation.value = ''
  } catch (error) {
    lifecycleError.value = safeMessage(error, 'The binding review could not be prepared.')
  } finally {
    lifecyclePending.value = false
  }
}

async function executeBinding(): Promise<void> {
  const item = selectedCertificate.value
  const plan = bindingPlan.value
  if (item === null || plan === null || bindingConfirmation.value !== item.primary_identifier) return
  lifecyclePending.value = true
  clearLifecycleResult()
  try {
    const task = await props.client.executeCertificateBindingPlan(plan.id, bindingConfirmation.value, csrfToken.value)
    replaceTask(task)
    selectedTask.value = task
    bindingPlan.value = null
    bindingConfirmation.value = ''
    bindingServerKeys.value = []
    lifecycleMessage.value = 'Binding task queued. The server will validate, publish, reload, and recover if needed.'
    startTaskStream(task.id)
  } catch (error) {
    lifecycleError.value = safeMessage(error, 'The binding task could not be queued.')
  } finally {
    lifecyclePending.value = false
  }
}

async function openExport(event: Event): Promise<void> {
  exportTrigger.value = event.currentTarget instanceof HTMLElement ? event.currentTarget : null
  exportConfirmation.value = ''
  includePrivateKey.value = false
  privateKeyConfirmation.value = ''
  exportOpen.value = true
  await nextTick()
  exportTrap.activate()
  exportCancelButton.value?.focus()
}

function closeExport(): void {
  exportTrap.deactivate()
  exportOpen.value = false
  exportConfirmation.value = ''
  includePrivateKey.value = false
  privateKeyConfirmation.value = ''
}

function handleExportKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    event.preventDefault()
    closeExport()
    return
  }
  exportTrap.onKeydown(event)
}

async function exportSelectedCertificate(): Promise<void> {
  const item = selectedCertificate.value
  if (item === null || !canExport.value) return
  lifecyclePending.value = true
  clearLifecycleResult()
  try {
    const file = await props.client.exportCertificate(item.id, {
      confirmation: exportConfirmation.value,
      include_private_key: includePrivateKey.value,
      private_key_confirmation: includePrivateKey.value ? privateKeyConfirmation.value : '',
    }, csrfToken.value)
    props.saveFile(file)
    closeExport()
    lifecycleMessage.value = 'Certificate export passed directly to the browser save boundary.'
  } catch (error) {
    lifecycleError.value = safeMessage(error, 'The certificate could not be exported.')
  } finally {
    lifecyclePending.value = false
  }
}

async function deleteSelectedCertificate(): Promise<void> {
  const item = selectedCertificate.value
  if (item === null || (item.bindings?.length ?? 0) > 0 || deleteConfirmation.value !== item.id) return
  lifecyclePending.value = true
  clearLifecycleResult()
  try {
    await props.client.deleteCertificate(item.id, deleteConfirmation.value, csrfToken.value)
    certificates.value = certificates.value.filter((candidate) => candidate.id !== item.id)
    selectedCertificate.value = null
    deleteConfirmation.value = ''
    lifecycleMessage.value = 'Unreferenced certificate material deleted.'
  } catch (error) {
    lifecycleError.value = safeMessage(error, 'The certificate could not be deleted.')
  } finally {
    lifecyclePending.value = false
  }
}

function replaceCertificate(item: CertificateRecord): void {
  selectedCertificate.value = item
  certificates.value = [item, ...certificates.value.filter((candidate) => candidate.id !== item.id)]
  autoRenew.value = item.auto_renew
  renewBeforeDays.value = Math.max(1, Math.round(item.renew_before_seconds / 86_400))
}

function clearLifecycleConfirmations(): void {
  renewConfirmation.value = ''
  policyConfirmation.value = ''
  unbindConfirmation.value = ''
  deleteConfirmation.value = ''
  bindingServerKeys.value = []
  bindingPlan.value = null
  bindingConfirmation.value = ''
  closeExport()
}

function clearLifecycleResult(): void {
  lifecycleError.value = ''
  lifecycleMessage.value = ''
}

async function openTask(id: string): Promise<void> {
  try {
    const task = await props.client.getCertificateTask(id)
    replaceTask(task)
    selectedTask.value = task
    if (isTerminalCertificateTask(task.state)) closeTaskStream()
    else startTaskStream(task.id)
  } catch (error) {
    pageError.value = safeMessage(error, 'Task evidence could not be loaded.')
  }
}

function startTaskStream(id: string): void {
  if (taskSource !== null && streamedTaskID === id) return
  closeTaskStream()
  streamedTaskID = id
  streamState.value = 'connecting'
  const source = props.eventSourceFactory(certificateTaskEventsPath(id))
  taskSource = source
  source.onopen = () => {
    streamState.value = 'connected'
    void refreshTask(id)
  }
  source.onmessage = (event) => {
    try {
      parseCertificateTaskStageEvent(event.data, 200)
      void refreshTask(id)
    } catch {
      streamState.value = 'reconnecting'
    }
  }
  source.onerror = () => {
    streamState.value = 'reconnecting'
    void refreshTask(id)
  }
}

async function refreshTask(id: string): Promise<void> {
  try {
    const task = await props.client.getCertificateTask(id)
    replaceTask(task)
    if (selectedTask.value?.id === id) selectedTask.value = task
    if (isTerminalCertificateTask(task.state)) closeTaskStream()
  } catch {
    streamState.value = 'reconnecting'
  }
}

async function cancelTask(): Promise<void> {
  const task = selectedTask.value
  if (task === null || isTerminalCertificateTask(task.state)) return
  taskPending.value = true
  try {
    const updated = await props.client.cancelCertificateTask(task.id, csrfToken.value)
    replaceTask(updated)
    selectedTask.value = updated
  } catch (error) {
    pageError.value = safeMessage(error, 'Cancellation could not be requested.')
  } finally {
    taskPending.value = false
  }
}

function replaceTask(task: CertificateTask): void {
  tasks.value = [task, ...tasks.value.filter((item) => item.id !== task.id)]
}

function closeTaskStream(): void {
  taskSource?.close()
  taskSource = null
  streamedTaskID = ''
  streamState.value = 'closed'
}

function safeMessage(error: unknown, fallback: string): string {
  if (!(error instanceof APIRequestError) || error.kind !== 'api' || error.apiError === undefined) {
    return fallback
  }
  const guidance = certificateErrorGuidance(error.apiError.code, fallback)
  return `${guidance} Request ID: ${error.apiError.request_id}.`
}

function certificateErrorGuidance(code: string, fallback: string): string {
  switch (code) {
    case 'CERTIFICATE_SERVICE_UNAVAILABLE':
    case 'AGENT_UNAVAILABLE':
      return 'The certificate service is unavailable. Retry after checking the Agent and network.'
    case 'ACME_RATE_LIMITED':
      return 'The certificate authority rate-limited this operation. Retry after the documented backoff.'
    case 'ACME_STAGING_PREFLIGHT_REQUIRED':
      return 'A successful staging preflight is required before this production request.'
    case 'ACME_TERMS_REQUIRED':
      return 'Accept the current ACME Terms of Service before retrying.'
    case 'ACME_ACCOUNT_DEACTIVATED':
      return 'The selected ACME account is deactivated. Select or create a valid account.'
    case 'CERTIFICATE_PLAN_EXPIRED':
      return 'This certificate review expired. Prepare and confirm a new review.'
    case 'CERTIFICATE_TASK_ACTIVE':
      return 'Another certificate task is active. Wait for its persisted terminal state.'
    case 'CERTIFICATE_REFERENCED':
      return 'The certificate is still referenced by Nginx. Remove its bindings before deletion.'
    case 'CERTIFICATE_NEEDS_ATTENTION':
    case 'CHALLENGE_CLEANUP_FAILED':
      return 'Certificate or challenge cleanup cannot be confirmed. Administrator attention is required.'
    case 'CERTIFICATE_BINDING_CONFLICT':
    case 'CERTIFICATE_SERVER_AMBIGUOUS':
    case 'CERTIFICATE_SERVER_NOT_FOUND':
      return 'The selected Nginx server evidence changed. Refresh and prepare a new binding review.'
    case 'CLOUDFLARE_TOKEN_INVALID':
      return 'The Cloudflare Token is invalid. Submit a new restricted Token.'
    case 'CLOUDFLARE_PERMISSION_DENIED':
      return 'The Cloudflare Token lacks Zone Read or DNS Write permission for this zone.'
    case 'CLOUDFLARE_ZONE_NOT_FOUND':
      return 'No matching Cloudflare zone was found for the requested identifier.'
    case 'CLOUDFLARE_UNAVAILABLE':
      return 'Cloudflare is unavailable. Retry without changing the current certificate.'
    case 'DNS_PROPAGATION_TIMEOUT':
      return 'DNS validation timed out. Check propagation before retrying.'
    case 'CERTIFICATE_OPERATION_TIMEOUT':
      return 'The certificate operation timed out. Refresh task evidence before retrying.'
    case 'CERTIFICATE_RESOURCE_NOT_FOUND':
      return 'The certificate resource no longer exists. Refresh the inventory.'
    case 'CERTIFICATE_WILDCARD_REQUIRES_DNS':
      return 'Wildcard certificates require Cloudflare DNS-01.'
    default:
      return fallback
  }
}

function environmentForAccount(id: string): string {
  const environment = accounts.value.find((account) => account.id === id)?.environment
  return environment === undefined ? 'Environment unavailable' : environmentLabel(environment)
}

function environmentLabel(value: CertificateEnvironment): string {
  return value === 'production' ? 'Production' : 'Staging'
}

function challengeLabel(value: CertificateChallenge): string {
  return value === 'http_01' ? 'HTTP-01' : 'Cloudflare DNS-01'
}

function stateLabel(value: CertificateState): string {
  return value.split('_').map((part) => part[0]?.toUpperCase() + part.slice(1)).join(' ')
}

function certificateTone(value: CertificateState): StatusTone {
  if (value === 'active') return 'success'
  if (value === 'expiring' || value === 'unbound') return 'warning'
  if (value === 'expired' || value === 'needs_attention') return 'error'
  return 'unknown'
}

function taskTone(value: CertificateTask['state']): StatusTone {
  if (value === 'succeeded') return 'success'
  if (value === 'failed' || value === 'needs_attention') return 'error'
  if (value === 'cancelling') return 'warning'
  return 'unknown'
}

function stageGroup(value: CertificateTaskStageName): string {
  if (value === 'provisioning' || value === 'propagating' || value === 'authorizing') return 'Domain validation'
  if (value === 'finalizing' || value === 'validating') return 'Certificate validation'
  if (value === 'deploying') return 'Nginx deployment'
  if (value === 'cleaning') return 'Challenge cleanup'
  return 'Task'
}

function abbreviate(value: string): string {
  return value.length <= 16 ? value : `${value.slice(0, 8)}…${value.slice(-8)}`
}

function formatTime(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}

function optionalTime(value: string | undefined): string {
  return value === undefined ? 'Not scheduled' : formatTime(value)
}
</script>

<style scoped>
.certificate-page,
.certificate-page__panel,
.certificate-workbench,
.certificate-detail,
.certificate-accounts__grid,
.certificate-history__layout {
  min-width: 0;
}

.certificate-page {
  display: grid;
  gap: var(--spacing-xl);
}

.certificate-page__header,
.certificate-page__panel > header,
.certificate-detail > header,
.certificate-history__detail > header {
  display: flex;
  min-width: 0;
  justify-content: space-between;
  align-items: flex-start;
  gap: var(--spacing-md);
}

.certificate-page__header h1,
.certificate-page__panel h2 {
  margin-block-end: var(--spacing-xs);
}

.certificate-page__eyebrow {
  margin-block-end: var(--spacing-xxs);
  color: var(--color-primary);
  font-size: var(--font-size-caption);
  font-weight: var(--font-weight-semibold);
}

.certificate-page button,
.certificate-page input,
.certificate-page select,
.certificate-list a {
  min-height: var(--component-control-min-size);
}

.certificate-page button {
  padding-inline: var(--spacing-md);
  border: 1px solid var(--color-primary);
  border-radius: var(--rounded-pill);
  background: var(--color-canvas);
  color: var(--color-primary);
  cursor: pointer;
}

.certificate-page button:disabled {
  border-color: var(--color-hairline);
  color: var(--color-ink-muted);
  cursor: default;
}

.certificate-page input,
.certificate-page select,
.certificate-page textarea {
  width: 100%;
  min-width: 0;
  padding: var(--spacing-xs) var(--spacing-sm);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas);
}

.certificate-page textarea {
  min-height: var(--component-certificate-secret-min-height);
  resize: vertical;
}

.certificate-page__tabs {
  display: flex;
  min-width: 0;
  overflow-x: auto;
  gap: var(--spacing-xs);
}

.certificate-page__tabs button[aria-pressed="true"] {
  background: var(--color-primary);
  color: var(--color-body-on-dark);
}

.certificate-page__error,
.certificate-page__blocking {
  padding: var(--spacing-sm);
  border: 1px solid var(--color-status-error-foreground);
  border-radius: var(--rounded-sm);
  background: var(--color-status-error-surface);
  color: var(--color-status-error-foreground);
}

.certificate-workbench {
  display: grid;
  grid-template-columns: var(--component-certificate-list-width) minmax(var(--component-certificate-detail-min-width), 1fr);
  gap: var(--spacing-lg);
}

.certificate-list,
.certificate-detail,
.certificate-request,
.certificate-accounts,
.certificate-history {
  padding: var(--spacing-lg);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  background: var(--color-canvas);
}

.certificate-list ul,
.certificate-detail ul,
.certificate-accounts ul,
.certificate-history ul,
.certificate-history ol {
  margin: 0;
  padding: 0;
  list-style: none;
}

.certificate-list a {
  display: grid;
  padding: var(--spacing-sm);
  border-bottom: 1px solid var(--color-hairline);
  color: var(--color-ink);
  gap: var(--spacing-xxs);
  text-decoration: none;
}

.certificate-list a[aria-current="page"] {
  border-inline-start: 3px solid var(--color-primary);
  color: var(--color-primary);
}

.certificate-list span,
.certificate-history__tasks span,
.certificate-accounts li span,
.certificate-request__servers small,
.certificate-history__timeline span {
  display: block;
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
}

.certificate-detail dl,
.certificate-request__review dl {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--spacing-sm);
}

.certificate-detail dl div,
.certificate-request__review dl div {
  min-width: 0;
}

.certificate-detail dt,
.certificate-request__review dt {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
}

.certificate-detail dd,
.certificate-request__review dd {
  margin: 0;
  overflow-wrap: anywhere;
}

.certificate-detail section {
  margin-block-start: var(--spacing-lg);
}

.certificate-detail__actions,
.certificate-detail__actions form,
.certificate-detail__utilities {
  display: grid;
  min-width: 0;
  gap: var(--spacing-xs);
}

.certificate-detail__actions form,
.certificate-detail__utilities {
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
}

.certificate-detail__check {
  display: grid;
  min-height: var(--component-control-min-size);
  grid-template-columns: 24px minmax(0, 1fr);
  align-items: center;
  gap: var(--spacing-xs);
}

.certificate-detail__check input {
  width: 20px;
  min-height: 20px;
}

.certificate-accounts a {
  display: inline-flex;
  min-height: var(--component-control-min-size);
  align-items: center;
}

.certificate-detail__sans {
  display: flex;
  flex-wrap: wrap;
  gap: var(--spacing-xs);
}

.certificate-request__steps {
  display: grid;
  margin: var(--spacing-lg) 0;
  padding: 0;
  grid-template-columns: repeat(6, minmax(0, 1fr));
  gap: var(--spacing-xs);
  list-style: none;
}

.certificate-request__steps li {
  display: grid;
  min-width: 0;
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
  gap: var(--spacing-xxs);
}

.certificate-request__steps li span {
  display: grid;
  width: var(--component-release-timeline-marker);
  height: var(--component-release-timeline-marker);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-pill);
  place-items: center;
}

.certificate-request__steps li[aria-current="step"] {
  color: var(--color-primary);
  font-weight: var(--font-weight-semibold);
}

.certificate-request form,
.certificate-request fieldset,
.certificate-accounts form,
.certificate-request__review > section {
  display: grid;
  min-width: 0;
  gap: var(--spacing-xs);
}

.certificate-request fieldset {
  margin: 0 0 var(--spacing-lg);
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
}

.certificate-request__domains {
  display: grid;
  max-height: var(--component-certificate-domain-max-height);
  overflow: auto;
  gap: var(--spacing-sm);
}

.certificate-request__domain {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: var(--spacing-xs);
}

.certificate-request__domain label {
  grid-column: 1 / -1;
}

.certificate-request__servers {
  display: grid;
  margin: 0;
  padding: 0;
  gap: var(--spacing-xs);
  list-style: none;
}

.certificate-request__servers label {
  display: grid;
  min-height: var(--component-control-min-size);
  grid-template-columns: var(--component-control-min-size) minmax(0, 1fr);
  align-items: center;
}

.certificate-request__servers input {
  width: 20px;
  justify-self: center;
}

.certificate-request__review {
  margin-block-start: var(--spacing-xl);
  padding-block-start: var(--spacing-lg);
  border-top: 1px solid var(--color-hairline);
}

.certificate-request__diff pre {
  max-height: 320px;
  padding: var(--spacing-sm);
  background: var(--color-diff-context);
  color: var(--color-diff-context-foreground);
  font: 13px/1.5 var(--font-code);
}

.certificate-accounts__grid,
.certificate-history__layout {
  display: grid;
  margin-block-start: var(--spacing-lg);
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--spacing-lg);
}

.certificate-accounts__grid > section,
.certificate-history__detail {
  min-width: 0;
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
}

.certificate-accounts__grid > section,
.certificate-accounts__grid form {
  display: grid;
  gap: var(--spacing-xs);
}

.certificate-accounts__grid form {
  margin-block-start: var(--spacing-lg);
  padding-block-start: var(--spacing-md);
  border-top: 1px solid var(--color-hairline);
}

.certificate-accounts__secret-help {
  min-height: var(--component-certificate-secret-min-height);
}

.certificate-accounts li,
.certificate-history__tasks button {
  display: grid;
  width: 100%;
  min-width: 0;
  padding: var(--spacing-sm);
  overflow-wrap: anywhere;
  gap: var(--spacing-xxs);
}

.certificate-history__tasks button {
  border: 0;
  border-bottom: 1px solid var(--color-hairline);
  border-radius: var(--rounded-none);
  text-align: start;
}

.certificate-history__timeline {
  display: grid;
  margin-block: var(--spacing-lg) !important;
  gap: var(--spacing-sm);
}

.certificate-history__timeline li {
  display: grid;
  grid-template-columns: var(--component-release-timeline-marker) minmax(0, 1fr);
  gap: var(--spacing-sm);
}

.certificate-page__visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}

.certificate-export__backdrop {
  position: fixed;
  z-index: var(--z-index-workspace-overlay);
  display: grid;
  overflow: auto;
  padding: var(--spacing-md);
  background: var(--color-workspace-backdrop);
  inset: 0;
  place-items: center;
}

.certificate-export {
  display: grid;
  width: var(--component-modal-width);
  max-width: 100%;
  min-width: 0;
  padding: var(--spacing-lg);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  background: var(--color-canvas);
  gap: var(--spacing-sm);
}

.certificate-export__actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--spacing-xs);
}

code {
  overflow-wrap: anywhere;
  font-family: var(--font-code);
}

@media (max-width: 1068px) {
  .certificate-workbench {
    grid-template-columns: minmax(240px, var(--component-certificate-list-width)) minmax(0, 1fr);
  }
}

@media (max-width: 833px) {
  .certificate-workbench,
  .certificate-accounts__grid,
  .certificate-history__layout {
    grid-template-columns: minmax(0, 1fr);
  }
}

@media (max-width: 640px) {
  .certificate-page__header,
  .certificate-page__panel > header,
  .certificate-detail > header,
  .certificate-history__detail > header {
    display: grid;
  }

  .certificate-page__panel[data-active="false"] {
    display: none;
  }

  .certificate-list,
  .certificate-detail,
  .certificate-request,
  .certificate-accounts,
  .certificate-history {
    padding: var(--spacing-md);
  }

  .certificate-request__steps {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }

  .certificate-detail dl,
  .certificate-request__review dl {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
