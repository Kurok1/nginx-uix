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
          {{ t('certificates.eyebrow') }}
        </p>
        <h1>{{ t('certificates.title') }}</h1>
        <p>{{ t('certificates.description') }}</p>
      </div>
      <button
        type="button"
        :disabled="loading"
        @click="refreshAll"
      >
        {{ loading ? t('certificates.refreshing') : t('certificates.refresh') }}
      </button>
    </header>

    <p
      v-if="pageError !== null"
      class="certificate-page__error"
      role="alert"
    >
      {{ messageText(pageError) }}
    </p>

    <nav
      class="certificate-page__tabs"
      :aria-label="t('certificates.tabsLabel')"
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
        {{ t('certificates.overview.title') }}
      </h2>
      <div class="certificate-workbench">
        <aside
          class="certificate-list"
          :aria-label="t('certificates.overview.listLabel')"
        >
          <p v-if="certificates.length === 0 && !loading">
            {{ t('certificates.overview.empty') }}
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
                <span>{{ t('certificates.overview.sanCount', { count: item.identifiers.length }) }} · {{ challengeLabel(item.challenge) }}</span>
                <span>{{ environmentForAccount(item.account_id) }} · {{ stateLabel(item.state) }}</span>
                <span>{{ t('certificates.overview.expires', { time: formatTime(item.not_after) }) }}</span>
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
            <div><dt>{{ t('certificates.overview.certificateId') }}</dt><dd><code>{{ selectedCertificate.id }}</code></dd></div>
            <div><dt>{{ t('certificates.overview.challenge') }}</dt><dd>{{ challengeLabel(selectedCertificate.challenge) }}</dd></div>
            <div><dt>{{ t('certificates.overview.validFrom') }}</dt><dd>{{ formatTime(selectedCertificate.not_before) }}</dd></div>
            <div><dt>{{ t('certificates.overview.validUntil') }}</dt><dd>{{ formatTime(selectedCertificate.not_after) }}</dd></div>
            <div><dt>{{ t('certificates.overview.activeVersion') }}</dt><dd><code>{{ abbreviate(selectedCertificate.active_version_id) }}</code></dd></div>
            <div><dt>{{ t('certificates.overview.automaticRenewal') }}</dt><dd>{{ selectedCertificate.auto_renew ? t('certificates.overview.enabled') : t('certificates.overview.disabled') }}</dd></div>
            <div><dt>{{ t('certificates.overview.nextAttempt') }}</dt><dd>{{ optionalTime(selectedCertificate.next_renewal_at) }}</dd></div>
          </dl>
          <section aria-labelledby="certificate-san-title">
            <h3 id="certificate-san-title">
              {{ t('certificates.overview.sans') }}
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
              {{ t('certificates.overview.bindings') }}
            </h3>
            <p v-if="(selectedCertificate.bindings?.length ?? 0) === 0">
              {{ t('certificates.overview.unbound') }}
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
            {{ t('certificates.overview.cleanupWarning') }}
          </p>

          <section
            class="certificate-detail__actions"
            aria-labelledby="certificate-lifecycle-title"
          >
            <h3 id="certificate-lifecycle-title">
              {{ t('certificates.lifecycle.title') }}
            </h3>
            <form
              data-action="renew-certificate"
              @submit.prevent="renewSelectedCertificate"
            >
              <label for="renew-confirmation">{{ t('certificates.lifecycle.renewConfirm', { name: selectedCertificate.primary_identifier }) }}</label>
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
                {{ t('certificates.lifecycle.renew') }}
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
                {{ t('certificates.lifecycle.autoRenew') }}
              </label>
              <label for="renew-before-days">{{ t('certificates.lifecycle.renewBefore') }}</label>
              <input
                id="renew-before-days"
                v-model.number="renewBeforeDays"
                name="renew-before-days"
                type="number"
                min="1"
                max="89"
              >
              <label for="renewal-policy-confirmation">{{ t('certificates.lifecycle.policyConfirm', { name: selectedCertificate.primary_identifier }) }}</label>
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
                {{ t('certificates.lifecycle.savePolicy') }}
              </button>
            </form>

            <form
              data-action="unbind-certificate"
              @submit.prevent="unbindSelectedCertificate"
            >
              <label for="unbind-confirmation">{{ t('certificates.lifecycle.unbindConfirm', { name: selectedCertificate.primary_identifier }) }}</label>
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
                {{ t('certificates.lifecycle.unbind') }}
              </button>
            </form>

            <section
              class="certificate-detail__binding"
              aria-labelledby="standalone-binding-title"
            >
              <h4 id="standalone-binding-title">
                {{ t('certificates.lifecycle.bindTitle') }}
              </h4>
              <p>{{ t('certificates.lifecycle.bindDescription') }}</p>
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
                      <strong>{{ candidate.ref.server_names.join(', ') || t('certificates.lifecycle.noServerName') }}</strong>
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
                {{ t('certificates.lifecycle.reviewBinding') }}
              </button>
              <div
                v-if="bindingPlan !== null"
                class="certificate-detail__binding-review"
              >
                <p>{{ t('certificates.lifecycle.bindingUnchanged') }}</p>
                <p>{{ t('certificates.lifecycle.planExpires', { time: formatTime(bindingPlan.expires_at) }) }} <code>{{ abbreviate(bindingPlan.production_digest) }}</code></p>
                <article
                  v-for="change in bindingPlan.binding_diff"
                  :key="change.path"
                  class="certificate-request__diff"
                >
                  <h4>{{ change.path }} · +{{ change.added_lines }} −{{ change.removed_lines }}</h4>
                  <pre
                    class="workspace-scroll-region"
                    :aria-label="t('certificates.lifecycle.bindingDiffLabel')"
                  >{{ change.patch }}</pre>
                </article>
                <label for="binding-confirmation">{{ t('certificates.lifecycle.bindingConfirm', { name: selectedCertificate.primary_identifier }) }}</label>
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
                  {{ t('certificates.lifecycle.bind') }}
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
                {{ t('certificates.lifecycle.export') }}
              </button>
              <p v-if="(selectedCertificate.bindings?.length ?? 0) > 0">
                {{ selectedCertificate.bindings?.length === 1
                  ? t('certificates.lifecycle.deleteBlockedOne')
                  : t('certificates.lifecycle.deleteBlockedMany', { count: selectedCertificate.bindings?.length ?? 0 }) }}
              </p>
              <label for="delete-confirmation">{{ t('certificates.lifecycle.deleteConfirm') }}</label>
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
                {{ t('certificates.lifecycle.delete') }}
              </button>
            </div>
            <p
              v-if="lifecycleMessage !== null"
              role="status"
            >
              {{ messageText(lifecycleMessage) }}
            </p>
            <p
              v-if="lifecycleError !== null"
              class="certificate-page__error"
              role="alert"
            >
              {{ messageText(lifecycleError) }}
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
                {{ t('certificates.export.title') }}
              </h3>
              <p>{{ t('certificates.export.description') }}</p>
              <label class="certificate-detail__check">
                <input
                  v-model="includePrivateKey"
                  name="include-private-key"
                  type="checkbox"
                >
                {{ t('certificates.export.includePrivateKey') }}
              </label>
              <p
                v-if="includePrivateKey"
                class="certificate-page__blocking"
              >
                {{ t('certificates.export.privateKeyWarning') }}
              </p>
              <label for="export-confirmation">{{ t('certificates.export.confirm') }}</label>
              <input
                id="export-confirmation"
                v-model="exportConfirmation"
                name="export-confirmation"
                type="text"
                autocomplete="off"
              >
              <template v-if="includePrivateKey">
                <label for="private-key-confirmation">{{ t('certificates.export.privateKeyConfirm') }}</label>
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
                  {{ t('certificates.export.cancel') }}
                </button>
                <button
                  type="button"
                  data-action="export-certificate"
                  :disabled="!canExport || lifecyclePending"
                  @click="exportSelectedCertificate"
                >
                  {{ t('certificates.export.action') }}
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
          {{ t('certificates.request.title') }}
        </h2>
        <p>{{ t('certificates.request.description') }}</p>
      </header>
      <ol
        class="certificate-request__steps"
        :aria-label="t('certificates.request.stepsLabel')"
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
          <legend>{{ t('certificates.request.identifiersLegend') }}</legend>
          <div class="certificate-request__domains">
            <div
              v-for="(_, index) in identifiers"
              :key="index"
              class="certificate-request__domain"
            >
              <label :for="`certificate-identifier-${index}`">{{ t('certificates.request.domain', { number: index + 1 }) }}</label>
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
                :aria-label="t('certificates.request.removeDomain', { number: index + 1 })"
                @click="removeIdentifier(index)"
              >
                {{ t('certificates.request.remove') }}
              </button>
            </div>
          </div>
          <button
            type="button"
            @click="addIdentifier"
          >
            {{ t('certificates.request.addDomain') }}
          </button>
        </fieldset>

        <fieldset>
          <legend>{{ t('certificates.request.challengeLegend') }}</legend>
          <label for="certificate-challenge">{{ t('certificates.request.validationMethod') }}</label>
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
            v-if="challengeError !== null"
            class="certificate-page__error"
            role="alert"
          >
            {{ messageText(challengeError) }}
          </p>
        </fieldset>

        <fieldset>
          <legend>{{ t('certificates.request.accountLegend') }}</legend>
          <label for="certificate-account">{{ t('certificates.request.account') }}</label>
          <select
            id="certificate-account"
            v-model="accountID"
            name="certificate-account"
            @change="invalidatePlan"
          >
            <option value="">
              {{ t('certificates.request.selectAccount') }}
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
            <label for="staging-account">{{ t('certificates.request.stagingAccount') }}</label>
            <select
              id="staging-account"
              v-model="stagingAccountID"
              name="staging-account"
              @change="invalidatePlan"
            >
              <option value="">
                {{ t('certificates.request.noStagingEvidence') }}
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
            <label for="dns-credential">{{ t('certificates.request.dnsCredential') }}</label>
            <select
              id="dns-credential"
              v-model="dnsCredentialID"
              name="dns-credential"
              @change="invalidatePlan"
            >
              <option value="">
                {{ t('certificates.request.selectCredential') }}
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
          <legend>{{ t('certificates.request.bindingsLegend') }}</legend>
          <p>{{ t('certificates.request.bindingsDescription') }}</p>
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
                  <strong>{{ candidate.ref.server_names.join(', ') || t('certificates.lifecycle.noServerName') }}</strong>
                  <small>{{ candidate.ref.listeners.join(', ') }} · {{ candidate.ref.path }}:{{ candidate.start_line }}</small>
                  <small>{{ candidate.editable ? t('certificates.request.editable') : candidate.read_only_reason }}</small>
                </span>
              </label>
            </li>
          </ul>
        </fieldset>

        <p
          v-if="wizardError !== null && wizardError.key !== challengeError?.key"
          class="certificate-page__error"
          role="alert"
        >
          {{ messageText(wizardError) }}
        </p>
        <button
          type="button"
          data-action="review-certificate"
          :disabled="wizardPending"
          @click="reviewCertificate"
        >
          {{ wizardPending ? t('certificates.request.preparing') : t('certificates.request.reviewAction') }}
        </button>
      </form>

      <section
        v-if="orderPlan !== null"
        class="certificate-request__review"
        aria-labelledby="certificate-review-title"
      >
        <h3 id="certificate-review-title">
          {{ t('certificates.request.reviewTitle') }}
        </h3>
        <p>{{ t('certificates.request.unchanged') }}</p>
        <dl>
          <div><dt>{{ t('certificates.request.environment') }}</dt><dd>{{ environmentLabel(orderPlan.environment) }}</dd></div>
          <div><dt>{{ t('certificates.request.identifiers') }}</dt><dd>{{ orderPlan.identifiers.join(', ') }}</dd></div>
          <div><dt>{{ t('certificates.request.challenge') }}</dt><dd>{{ challengeLabel(orderPlan.challenge) }}</dd></div>
          <div><dt>{{ t('certificates.request.servers') }}</dt><dd>{{ orderPlan.server_refs.length }}</dd></div>
          <div><dt>{{ t('certificates.request.productionIdentity') }}</dt><dd><code>{{ abbreviate(orderPlan.production_digest) }}</code></dd></div>
          <div><dt>{{ t('certificates.request.planExpires') }}</dt><dd>{{ formatTime(orderPlan.expires_at) }}</dd></div>
        </dl>
        <p
          v-if="orderPlan.environment === 'production' && !orderPlan.staging_evidence"
          class="certificate-page__blocking"
        >
          {{ t('certificates.request.stagingRequired') }}
        </p>
        <article
          v-for="change in orderPlan.binding_diff"
          :key="change.path"
          class="certificate-request__diff"
        >
          <h4>{{ change.path }} · +{{ change.added_lines }} −{{ change.removed_lines }}</h4>
          <pre
            class="workspace-scroll-region"
            :aria-label="t('certificates.request.diffLabel')"
          >{{ change.patch }}</pre>
        </article>

        <section aria-labelledby="certificate-confirm-title">
          <h3 id="certificate-confirm-title">
            {{ t('certificates.request.confirmTitle') }}
          </h3>
          <p>{{ t('certificates.request.consequence') }}</p>
          <label for="certificate-confirmation">{{ t('certificates.request.exactConfirm', { name: orderPlan.primary_identifier }) }}</label>
          <input
            id="certificate-confirmation"
            v-model="confirmation"
            name="certificate-confirmation"
            type="text"
            autocomplete="off"
          >
          <template v-if="orderPlan.requires_risk_confirmation">
            <p class="certificate-page__blocking">
              {{ t('certificates.request.rateLimitWarning') }}
            </p>
            <label for="production-risk-confirmation">{{ t('certificates.request.riskConfirm', { phrase: orderPlan.risk_confirmation_phrase }) }}</label>
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
            {{ wizardPending ? t('certificates.request.queueing') : t('certificates.request.issue') }}
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
          {{ t('certificates.accounts.title') }}
        </h2>
        <p>{{ t('certificates.accounts.description') }}</p>
      </header>
      <div class="certificate-accounts__grid">
        <section aria-labelledby="acme-accounts-title">
          <h3 id="acme-accounts-title">
            {{ t('certificates.accounts.acmeTitle') }}
          </h3>
          <ul>
            <li
              v-for="account in accounts"
              :key="account.id"
            >
              <strong>{{ environmentLabel(account.environment) }}</strong> · {{ account.email }}
              <span>{{ accountStatusLabel(account.status) }} · <code>{{ abbreviate(account.id) }}</code></span>
              <a
                :href="account.terms_url"
                target="_blank"
                rel="noreferrer"
              >{{ t('certificates.accounts.currentTerms') }}</a>
              <button
                v-if="account.status === 'valid'"
                type="button"
                data-action="deactivate-account"
                :data-id="account.id"
                @click="openAccountDeactivation(account, $event)"
              >
                {{ t('certificates.accounts.deactivate') }}
              </button>
            </li>
          </ul>
          <p v-if="accounts.length === 0">
            {{ t('certificates.accounts.empty') }}
          </p>
          <form
            data-action="create-acme-account"
            @submit.prevent="createAccount"
          >
            <h4>{{ t('certificates.accounts.createTitle') }}</h4>
            <label for="account-environment">{{ t('certificates.accounts.environment') }}</label>
            <select
              id="account-environment"
              v-model="accountEnvironment"
              name="account-environment"
            >
              <option value="staging">
                {{ t('certificates.accounts.staging') }}
              </option>
              <option value="production">
                {{ t('certificates.accounts.production') }}
              </option>
            </select>
            <label for="account-email">{{ t('certificates.accounts.email') }}</label>
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
                {{ t('certificates.accounts.agreeBefore') }}
                <a
                  :href="termsURL(accountEnvironment)"
                  target="_blank"
                  rel="noreferrer"
                >{{ t('certificates.accounts.terms') }}</a>
              </span>
            </label>
            <button
              type="submit"
              :disabled="accountPending || accountEmail.trim() === '' || !accountTermsAccepted"
            >
              {{ accountPending ? t('certificates.accounts.creating') : t('certificates.accounts.create') }}
            </button>
          </form>

          <form
            data-action="import-acme-account"
            @submit.prevent="importAccount"
          >
            <h4>{{ t('certificates.accounts.importTitle') }}</h4>
            <label for="import-environment">{{ t('certificates.accounts.environment') }}</label>
            <select
              id="import-environment"
              v-model="importEnvironment"
              name="import-environment"
            >
              <option value="staging">
                {{ t('certificates.accounts.staging') }}
              </option>
              <option value="production">
                {{ t('certificates.accounts.production') }}
              </option>
            </select>
            <label for="import-email">{{ t('certificates.accounts.email') }}</label>
            <input
              id="import-email"
              v-model="importEmail"
              name="import-email"
              type="email"
              autocomplete="email"
            >
            <label for="import-account-uri">{{ t('certificates.accounts.accountUri') }}</label>
            <input
              id="import-account-uri"
              v-model="importAccountURI"
              name="import-account-uri"
              type="url"
              autocomplete="off"
              spellcheck="false"
            >
            <label for="import-private-key">{{ t('certificates.accounts.privateKey') }}</label>
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
                {{ t('certificates.accounts.agreeBefore') }}
                <a
                  :href="termsURL(importEnvironment)"
                  target="_blank"
                  rel="noreferrer"
                >{{ t('certificates.accounts.terms') }}</a>
              </span>
            </label>
            <button
              type="submit"
              :disabled="accountPending || importEmail.trim() === '' || importAccountURI.trim() === '' || importPrivateKey === '' || !importTermsAccepted"
            >
              {{ accountPending ? t('certificates.accounts.importing') : t('certificates.accounts.import') }}
            </button>
          </form>
          <p
            v-if="accountMessage !== null"
            role="status"
          >
            {{ messageText(accountMessage) }}
          </p>
          <p
            v-if="accountError !== null"
            class="certificate-page__error"
            role="alert"
          >
            {{ messageText(accountError) }}
          </p>
        </section>

        <section aria-labelledby="cloudflare-credentials-title">
          <h3 id="cloudflare-credentials-title">
            {{ t('certificates.cloudflare.title') }}
          </h3>
          <p class="certificate-accounts__secret-help">
            {{ t('certificates.cloudflare.grantOnly') }} <strong>Zone Read</strong> {{ t('certificates.cloudflare.and') }} <strong>DNS Write</strong>{{ t('certificates.cloudflare.guidance') }}
          </p>
          <form
            data-action="save-cloudflare-token"
            @submit.prevent="saveCloudflareCredential"
          >
            <label for="credential-name">{{ t('certificates.cloudflare.name') }}</label>
            <input
              id="credential-name"
              v-model="credentialName"
              name="credential-name"
              type="text"
              autocomplete="off"
              maxlength="128"
            >
            <label for="cloudflare-token">{{ t('certificates.cloudflare.token') }}</label>
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
              {{ credentialPending ? t('certificates.cloudflare.verifying') : t('certificates.cloudflare.save') }}
            </button>
          </form>
          <p
            v-if="credentialMessage !== null"
            role="status"
          >
            {{ messageText(credentialMessage) }}
          </p>
          <p
            v-if="credentialError !== null"
            class="certificate-page__error"
            role="alert"
          >
            {{ messageText(credentialError) }}
          </p>
          <ul class="certificate-accounts__credentials">
            <li
              v-for="item in credentials"
              :key="item.id"
            >
              <strong>{{ item.name }}</strong>
              <span>{{ t('certificates.cloudflare.fingerprint', { status: credentialStatusLabel(item.status) }) }} <code>{{ item.fingerprint }}</code></span>
              <span>{{ t('certificates.cloudflare.verified', { time: formatTime(item.verified_at) }) }}</span>
              <button
                v-if="item.status !== 'deleted'"
                type="button"
                data-action="delete-dns-credential"
                :data-id="item.id"
                @click="openCredentialDeletion(item, $event)"
              >
                {{ t('certificates.cloudflare.delete') }}
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
          {{ t('certificates.history.title') }}
        </h2>
        <p>{{ t('certificates.history.description') }}</p>
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
              <strong>{{ taskKindLabel(task.kind) }} · {{ taskStateLabel(task.state) }}</strong>
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
                {{ t('certificates.history.taskTitle', { kind: taskKindLabel(selectedTask.kind) }) }}
              </h3>
              <code>{{ selectedTask.id }}</code>
            </div>
            <StatusBadge
              :tone="taskTone(selectedTask.state)"
              :label="taskStateLabel(selectedTask.state)"
            />
          </header>
          <p
            aria-live="polite"
            aria-atomic="true"
          >
            {{ currentTaskPhrase }}
          </p>
          <p>{{ t('certificates.history.stream', { state: streamLabel }) }}</p>
          <p
            v-if="selectedTask.state === 'needs_attention'"
            class="certificate-page__blocking"
            role="alert"
          >
            {{ t('certificates.overview.cleanupWarning') }}
          </p>
          <ol class="certificate-history__timeline">
            <li
              v-for="stage in selectedTask.stages"
              :key="stage.sequence"
            >
              <span aria-hidden="true">◇</span>
              <div><strong>{{ stageGroup(stage.stage) }} · {{ taskStageLabel(stage.stage) }}</strong><span>{{ taskResultLabel(stage.result) }} · {{ formatTime(stage.occurred_at) }}</span></div>
            </li>
          </ol>
          <button
            v-if="!isTerminalCertificateTask(selectedTask.state)"
            type="button"
            :disabled="taskPending"
            @click="cancelTask"
          >
            {{ selectedTask.state === 'cancelling' ? t('certificates.history.canceling') : t('certificates.history.cancel') }}
          </button>
        </article>
      </div>
    </section>

    <OperationConfirmModal
      :open="accountDeactivateTarget !== null"
      :title="t('certificates.modals.deactivateTitle')"
      :consequence="t('certificates.modals.deactivateConsequence')"
      :confirmation-text="accountDeactivateTarget?.id ?? ''"
      :confirm-label="t('certificates.modals.deactivateAction')"
      :requires-reason="false"
      :pending="accountPending"
      :trigger="accountModalTrigger"
      @cancel="closeAccountDeactivation"
      @confirm="deactivateAccount"
    />
    <OperationConfirmModal
      :open="credentialDeleteTarget !== null"
      :title="t('certificates.modals.deleteCredentialTitle')"
      :consequence="t('certificates.modals.deleteCredentialConsequence')"
      :confirmation-text="credentialDeleteTarget?.id ?? ''"
      :confirm-label="t('certificates.modals.deleteCredentialAction')"
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
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import {
  certificateTaskEventsPath,
  isTerminalCertificateTask,
  parseCertificateTaskStageEvent,
  type ACMEAccount,
  type ACMEAccountStatus,
  type ACMEDirectory,
  type CertificateChallenge,
  type CertificateBindingPlan,
  type CertificateEnvironment,
  type CertificateOrderPlan,
  type CertificateRecord,
  type CertificateServerCandidate,
  type CertificateState,
  type CertificateStageResult,
  type CertificateTask,
  type CertificateTaskKind,
  type CertificateTaskState,
  type CertificateTaskStageName,
  type DNSCredential,
  type DNSCredentialStatus,
} from '../api/certificates'
import { APIRequestError, apiClient, type APIClient } from '../api/client'
import OperationConfirmModal from '../components/OperationConfirmModal.vue'
import StatusBadge, { type StatusTone } from '../components/StatusBadge.vue'
import { useFocusTrap } from '../composables/useFocusTrap'
import { sessionStore } from '../session'

const { d, t } = useI18n()

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

interface LocalizedMessage {
  key: string
  values?: Record<string, string | number>
  requestId?: string
  environment?: CertificateEnvironment
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
const tabs = computed<ReadonlyArray<{ id: Tab; label: string }>>(() => [
  { id: 'overview', label: t('certificates.tabs.overview') },
  { id: 'request', label: t('certificates.tabs.request') },
  { id: 'accounts', label: t('certificates.tabs.accounts') },
  { id: 'history', label: t('certificates.tabs.history') },
])
const wizardSteps = computed(() => [
  t('certificates.request.steps.identifiers'),
  t('certificates.request.steps.challenge'),
  t('certificates.request.steps.account'),
  t('certificates.request.steps.bindings'),
  t('certificates.request.steps.review'),
  t('certificates.request.steps.confirm'),
])

const activeTab = ref<Tab>('overview')
const loading = ref(true)
const pageError = ref<LocalizedMessage | null>(null)
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
const wizardError = ref<LocalizedMessage | null>(null)
const wizardPending = ref(false)

const credentialName = ref('')
const cloudflareToken = ref('')
const credentialPending = ref(false)
const credentialError = ref<LocalizedMessage | null>(null)
const credentialMessage = ref<LocalizedMessage | null>(null)

const accountEnvironment = ref<CertificateEnvironment>('staging')
const accountEmail = ref('')
const accountTermsAccepted = ref(false)
const importEnvironment = ref<CertificateEnvironment>('staging')
const importEmail = ref('')
const importAccountURI = ref('')
const importPrivateKey = ref('')
const importTermsAccepted = ref(false)
const accountPending = ref(false)
const accountError = ref<LocalizedMessage | null>(null)
const accountMessage = ref<LocalizedMessage | null>(null)
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
const lifecycleError = ref<LocalizedMessage | null>(null)
const lifecycleMessage = ref<LocalizedMessage | null>(null)
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
    ? localMessage('certificates.validation.wildcard')
    : null,
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
const currentTaskPhrase = computed(() => selectedTask.value === null
  ? ''
  : `${taskStageLabel(selectedTask.value.stage)} — ${taskStateLabel(selectedTask.value.state)}`)
const streamLabel = computed(() => ({
  closed: t('certificates.history.streamStates.closed'),
  connecting: t('certificates.history.streamStates.connecting'),
  connected: t('certificates.history.streamStates.connected'),
  reconnecting: t('certificates.history.streamStates.reconnecting'),
})[streamState.value])

onMounted(() => void refreshAll())
onBeforeUnmount(closeTaskStream)
watch(() => props.certificateId, (id) => {
  if (id !== '') void openCertificate(id)
})

async function refreshAll(): Promise<void> {
  loading.value = true
  pageError.value = null
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
    pageError.value = safeMessage(error, 'certificates.errors.evidence')
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
    pageError.value = safeMessage(error, 'certificates.errors.detail')
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
  wizardError.value = null
  wizardStep.value = 1
}

async function reviewCertificate(): Promise<void> {
  wizardError.value = validateWizard()
  if (wizardError.value !== null) return
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
    wizardError.value = safeMessage(error, 'certificates.errors.review')
  } finally {
    wizardPending.value = false
  }
}

function validateWizard(): LocalizedMessage | null {
  if (normalizedIdentifiers.value.length === 0) return localMessage('certificates.validation.domainRequired')
  if (new Set(normalizedIdentifiers.value).size !== normalizedIdentifiers.value.length) {
    return localMessage('certificates.validation.duplicateDomains')
  }
  if (normalizedIdentifiers.value.some((identifier) => !validIdentifier(identifier))) {
    return localMessage('certificates.validation.invalidDomains')
  }
  if (challengeError.value !== null) return challengeError.value
  if (accountID.value === '') return localMessage('certificates.validation.accountRequired')
  if (challenge.value === 'cloudflare_dns_01' && dnsCredentialID.value === '') {
    return localMessage('certificates.validation.credentialRequired')
  }
  if (selectedServerFingerprints.value.length === 0) {
    return localMessage('certificates.validation.serverRequired')
  }
  return null
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
    wizardError.value = safeMessage(error, 'certificates.errors.queue')
  } finally {
    wizardPending.value = false
  }
}

async function saveCloudflareCredential(): Promise<void> {
  const name = credentialName.value.trim()
  const token = cloudflareToken.value
  if (name === '' || token === '') return
  credentialPending.value = true
  credentialError.value = null
  credentialMessage.value = null
  try {
    const item = await props.client.createCertificateDNSCredential({ name, api_token: token }, csrfToken.value)
    cloudflareToken.value = ''
    credentialName.value = ''
    credentials.value = [item, ...credentials.value.filter((existing) => existing.id !== item.id)]
    dnsCredentialID.value = item.id
    credentialMessage.value = localMessage('certificates.messages.credentialSaved')
  } catch (error) {
    credentialError.value = safeMessage(error, 'certificates.errors.tokenVerify')
  } finally {
    credentialPending.value = false
  }
}

async function createAccount(): Promise<void> {
  const email = accountEmail.value.trim()
  if (email === '' || !accountTermsAccepted.value) return
  accountPending.value = true
  accountError.value = null
  accountMessage.value = null
  try {
    const account = await props.client.createACMEAccount({
      environment: accountEnvironment.value,
      email,
      terms_of_service_agreed: true,
    }, csrfToken.value)
    accounts.value = [account, ...accounts.value.filter((item) => item.id !== account.id)]
    accountEmail.value = ''
    accountTermsAccepted.value = false
    accountMessage.value = localMessage('certificates.messages.accountCreated', undefined, account.environment)
    setAccountDefaults()
  } catch (error) {
    accountError.value = safeMessage(error, 'certificates.errors.accountCreate')
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
  accountError.value = null
  accountMessage.value = null
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
    accountMessage.value = localMessage('certificates.messages.accountImported', undefined, account.environment)
    setAccountDefaults()
  } catch (error) {
    accountError.value = safeMessage(error, 'certificates.errors.accountImport')
  } finally {
    accountPending.value = false
  }
}

function openAccountDeactivation(account: ACMEAccount, event: Event): void {
  accountModalTrigger.value = event.currentTarget instanceof HTMLElement ? event.currentTarget : null
  accountDeactivateTarget.value = account
  accountError.value = null
}

function closeAccountDeactivation(): void {
  accountDeactivateTarget.value = null
}

async function deactivateAccount(_reason: string, confirmation: string): Promise<void> {
  const account = accountDeactivateTarget.value
  if (account === null || confirmation !== account.id) return
  accountPending.value = true
  accountError.value = null
  accountMessage.value = null
  try {
    const updated = await props.client.deactivateACMEAccount(account.id, csrfToken.value)
    accounts.value = accounts.value.map((item) => item.id === updated.id ? updated : item)
    closeAccountDeactivation()
    accountMessage.value = localMessage('certificates.messages.accountDeactivated')
    setAccountDefaults()
  } catch (error) {
    accountError.value = safeMessage(error, 'certificates.errors.accountDeactivate')
  } finally {
    accountPending.value = false
  }
}

function openCredentialDeletion(item: DNSCredential, event: Event): void {
  credentialModalTrigger.value = event.currentTarget instanceof HTMLElement ? event.currentTarget : null
  credentialDeleteTarget.value = item
  credentialError.value = null
}

function closeCredentialDeletion(): void {
  credentialDeleteTarget.value = null
}

async function deleteCredential(_reason: string, confirmation: string): Promise<void> {
  const item = credentialDeleteTarget.value
  if (item === null || confirmation !== item.id) return
  credentialPending.value = true
  credentialError.value = null
  credentialMessage.value = null
  try {
    await props.client.deleteCertificateDNSCredential(item.id, csrfToken.value)
    credentials.value = credentials.value.filter((credential) => credential.id !== item.id)
    closeCredentialDeletion()
    credentialMessage.value = localMessage('certificates.messages.credentialDeleted')
    setAccountDefaults()
  } catch (error) {
    credentialError.value = safeMessage(error, 'certificates.errors.credentialDelete')
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
    lifecycleMessage.value = localMessage('certificates.messages.renewalQueued')
    startTaskStream(task.id)
  } catch (error) {
    lifecycleError.value = safeMessage(error, 'certificates.errors.renewalQueue')
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
    lifecycleMessage.value = localMessage('certificates.messages.policyUpdated')
  } catch (error) {
    lifecycleError.value = safeMessage(error, 'certificates.errors.policyUpdate')
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
    lifecycleMessage.value = localMessage('certificates.messages.unbound')
  } catch (error) {
    lifecycleError.value = safeMessage(error, 'certificates.errors.unbind')
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
    lifecycleError.value = safeMessage(error, 'certificates.errors.bindingReview')
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
    lifecycleMessage.value = localMessage('certificates.messages.bindingQueued')
    startTaskStream(task.id)
  } catch (error) {
    lifecycleError.value = safeMessage(error, 'certificates.errors.bindingQueue')
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
    lifecycleMessage.value = localMessage('certificates.messages.exported')
  } catch (error) {
    lifecycleError.value = safeMessage(error, 'certificates.errors.export')
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
    lifecycleMessage.value = localMessage('certificates.messages.deleted')
  } catch (error) {
    lifecycleError.value = safeMessage(error, 'certificates.errors.delete')
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
  lifecycleError.value = null
  lifecycleMessage.value = null
}

async function openTask(id: string): Promise<void> {
  try {
    const task = await props.client.getCertificateTask(id)
    replaceTask(task)
    selectedTask.value = task
    if (isTerminalCertificateTask(task.state)) closeTaskStream()
    else startTaskStream(task.id)
  } catch (error) {
    pageError.value = safeMessage(error, 'certificates.errors.taskEvidence')
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
    pageError.value = safeMessage(error, 'certificates.errors.cancellation')
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

function localMessage(
  key: string,
  values?: Record<string, string | number>,
  environment?: CertificateEnvironment,
  requestId?: string,
): LocalizedMessage {
  return {
    key,
    ...(values === undefined ? {} : { values }),
    ...(environment === undefined ? {} : { environment }),
    ...(requestId === undefined ? {} : { requestId }),
  }
}

function messageText(message: LocalizedMessage): string {
  const values = message.environment === undefined
    ? message.values
    : { ...message.values, environment: environmentLabel(message.environment) }
  const translated = t(message.key, values ?? {})
  return message.requestId === undefined
    ? translated
    : t('errors.withRequestId', { message: translated, requestId: message.requestId })
}

function safeMessage(error: unknown, fallbackKey: string): LocalizedMessage {
  if (!(error instanceof APIRequestError)) {
    return localMessage(fallbackKey)
  }
  if (error.kind !== 'api' || error.apiError === undefined) {
    return localMessage(fallbackKey, undefined, undefined, error.requestID)
  }
  const guidanceKey = certificateErrorGuidance(error.apiError.code, fallbackKey)
  return localMessage(guidanceKey, undefined, undefined, error.apiError.request_id)
}

function certificateErrorGuidance(code: string, fallbackKey: string): string {
  switch (code) {
    case 'CERTIFICATE_SERVICE_UNAVAILABLE':
    case 'AGENT_UNAVAILABLE':
      return 'certificates.errors.unavailable'
    case 'ACME_RATE_LIMITED':
      return 'certificates.errors.rateLimited'
    case 'ACME_STAGING_PREFLIGHT_REQUIRED':
      return 'certificates.errors.stagingRequired'
    case 'ACME_TERMS_REQUIRED':
      return 'certificates.errors.termsRequired'
    case 'ACME_ACCOUNT_DEACTIVATED':
      return 'certificates.errors.accountDeactivated'
    case 'CERTIFICATE_PLAN_EXPIRED':
      return 'certificates.errors.planExpired'
    case 'CERTIFICATE_TASK_ACTIVE':
      return 'certificates.errors.taskActive'
    case 'CERTIFICATE_REFERENCED':
      return 'certificates.errors.referenced'
    case 'CERTIFICATE_NEEDS_ATTENTION':
    case 'CHALLENGE_CLEANUP_FAILED':
      return 'certificates.errors.cleanup'
    case 'CERTIFICATE_BINDING_CONFLICT':
    case 'CERTIFICATE_SERVER_AMBIGUOUS':
    case 'CERTIFICATE_SERVER_NOT_FOUND':
      return 'certificates.errors.serverChanged'
    case 'CLOUDFLARE_TOKEN_INVALID':
      return 'certificates.errors.tokenInvalid'
    case 'CLOUDFLARE_PERMISSION_DENIED':
      return 'certificates.errors.permissionDenied'
    case 'CLOUDFLARE_ZONE_NOT_FOUND':
      return 'certificates.errors.zoneNotFound'
    case 'CLOUDFLARE_UNAVAILABLE':
      return 'certificates.errors.cloudflareUnavailable'
    case 'DNS_PROPAGATION_TIMEOUT':
      return 'certificates.errors.dnsTimeout'
    case 'CERTIFICATE_OPERATION_TIMEOUT':
      return 'certificates.errors.operationTimeout'
    case 'CERTIFICATE_RESOURCE_NOT_FOUND':
      return 'certificates.errors.notFound'
    case 'CERTIFICATE_WILDCARD_REQUIRES_DNS':
      return 'certificates.errors.wildcard'
    default:
      return fallbackKey
  }
}

function environmentForAccount(id: string): string {
  const environment = accounts.value.find((account) => account.id === id)?.environment
  return environment === undefined ? t('certificates.labels.environmentUnavailable') : environmentLabel(environment)
}

function environmentLabel(value: CertificateEnvironment): string {
  return value === 'production'
    ? t('certificates.labels.production')
    : t('certificates.labels.staging')
}

function challengeLabel(value: CertificateChallenge): string {
  return value === 'http_01' ? 'HTTP-01' : 'Cloudflare DNS-01'
}

function stateLabel(value: CertificateState): string {
  const labels: Record<CertificateState, string> = {
    pending: t('certificates.labels.certificateStates.pending'),
    active: t('certificates.labels.certificateStates.active'),
    expiring: t('certificates.labels.certificateStates.expiring'),
    expired: t('certificates.labels.certificateStates.expired'),
    unbound: t('certificates.labels.certificateStates.unbound'),
    needs_attention: t('certificates.labels.certificateStates.needsAttention'),
    deleted: t('certificates.labels.certificateStates.deleted'),
  }
  return labels[value]
}

function accountStatusLabel(value: ACMEAccountStatus): string {
  const labels: Record<ACMEAccountStatus, string> = {
    valid: t('certificates.accounts.statuses.valid'),
    deactivating: t('certificates.accounts.statuses.deactivating'),
    deactivated: t('certificates.accounts.statuses.deactivated'),
  }
  return labels[value]
}

function credentialStatusLabel(value: DNSCredentialStatus): string {
  const labels: Record<DNSCredentialStatus, string> = {
    valid: t('certificates.cloudflare.statuses.valid'),
    needs_attention: t('certificates.cloudflare.statuses.needsAttention'),
    deleted: t('certificates.cloudflare.statuses.deleted'),
  }
  return labels[value]
}

function taskKindLabel(value: CertificateTaskKind): string {
  const labels: Record<CertificateTaskKind, string> = {
    issue: t('certificates.history.kinds.issue'),
    renew: t('certificates.history.kinds.renew'),
    bind: t('certificates.history.kinds.bind'),
    unbind: t('certificates.history.kinds.unbind'),
  }
  return labels[value]
}

function taskStateLabel(value: CertificateTaskState): string {
  const labels: Record<CertificateTaskState, string> = {
    queued: t('certificates.history.states.queued'),
    running: t('certificates.history.states.running'),
    cancelling: t('certificates.history.states.cancelling'),
    succeeded: t('certificates.history.states.succeeded'),
    failed: t('certificates.history.states.failed'),
    cancelled: t('certificates.history.states.cancelled'),
    needs_attention: t('certificates.history.states.needsAttention'),
  }
  return labels[value]
}

function taskStageLabel(value: CertificateTaskStageName): string {
  const labels: Record<CertificateTaskStageName, string> = {
    queued: t('certificates.history.stages.queued'),
    preparing: t('certificates.history.stages.preparing'),
    ordering: t('certificates.history.stages.ordering'),
    provisioning: t('certificates.history.stages.provisioning'),
    propagating: t('certificates.history.stages.propagating'),
    authorizing: t('certificates.history.stages.authorizing'),
    finalizing: t('certificates.history.stages.finalizing'),
    validating: t('certificates.history.stages.validating'),
    deploying: t('certificates.history.stages.deploying'),
    cleaning: t('certificates.history.stages.cleaning'),
    completed: t('certificates.history.stages.completed'),
    failed: t('certificates.history.stages.failed'),
    cancelled: t('certificates.history.stages.cancelled'),
    needs_attention: t('certificates.history.stages.needsAttention'),
  }
  return labels[value]
}

function taskResultLabel(value: CertificateStageResult): string {
  const labels: Record<CertificateStageResult, string> = {
    pending: t('certificates.history.results.pending'),
    running: t('certificates.history.results.running'),
    success: t('certificates.history.results.success'),
    failed: t('certificates.history.results.failed'),
    warning: t('certificates.history.results.warning'),
  }
  return labels[value]
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
  if (value === 'provisioning' || value === 'propagating' || value === 'authorizing') {
    return t('certificates.history.groups.domainValidation')
  }
  if (value === 'finalizing' || value === 'validating') {
    return t('certificates.history.groups.certificateValidation')
  }
  if (value === 'deploying') return t('certificates.history.groups.deployment')
  if (value === 'cleaning') return t('certificates.history.groups.cleanup')
  return t('certificates.history.groups.task')
}

function abbreviate(value: string): string {
  return value.length <= 16 ? value : `${value.slice(0, 8)}…${value.slice(-8)}`
}

function formatTime(value: string): string {
  return d(new Date(value), 'short')
}

function optionalTime(value: string | undefined): string {
  return value === undefined ? t('certificates.labels.notScheduled') : formatTime(value)
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
