<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import ApproveStep from '../components/ApproveStep.vue'
import AudienceStep from '../components/AudienceStep.vue'
import BriefStep from '../components/BriefStep.vue'
import ContentStep from '../components/ContentStep.vue'
import ErrorNotice from '../components/ErrorNotice.vue'
import StatusPill from '../components/StatusPill.vue'
import StepNav from '../components/StepNav.vue'
import { useComposer, type Step } from '../lib/composer'
import { links, navigate, replaceHash } from '../lib/router'

const props = defineProps<{ campaignId: string | null }>()
const c = useComposer(props.campaignId)
const approveStep = ref<InstanceType<typeof ApproveStep> | null>(null)

const reachable = computed<Record<Step, boolean>>(() => ({
  brief: true,
  audience: !!c.campaign.value?.segment,
  content: !!c.campaign.value?.segment,
  approve: !!c.campaign.value?.content,
}))

// A campaign that is already sending belongs on the dashboard.
watch(
  () => c.campaign.value?.status,
  (s) => {
    if (c.campaign.value && (s === 'sending' || s === 'completed' || s === 'completed_with_failures')) {
      navigate(links.live(c.campaign.value.id))
    }
  },
)

// Keep the address bar on the campaign once it exists, so a reload resumes here.
watch(
  () => c.campaign.value?.id,
  (id) => {
    if (id && props.campaignId === null) replaceHash(links.composer(id))
  },
)

async function confirmApprove() {
  const ok = await c.approve()
  approveStep.value?.close()
  if (ok && c.campaign.value) navigate(links.live(c.campaign.value.id))
}

const staleConflict = computed(() => c.error.value?.code === 'stale_content_version')
const alreadyApproved = computed(() => c.error.value?.code === 'already_approved')
</script>

<template>
  <div class="shell">
    <div class="page-head">
      <div>
        <h1>{{ campaignId || c.campaign.value ? 'Campaign' : 'New campaign' }}</h1>
        <p v-if="c.campaign.value" class="row" style="gap: 8px">
          <StatusPill :status="c.campaign.value.status" />
          <span class="muted">{{ c.campaign.value.id }}</span>
        </p>
      </div>
    </div>

    <div v-if="c.loading.value" class="empty" role="status">
      <span class="spinner" aria-hidden="true" />
      <p>Loading campaign…</p>
    </div>

    <template v-else>
      <StepNav :current="c.step.value" :reachable="reachable" @go="c.goTo" />

      <div v-if="c.error.value" class="stack" style="margin-bottom: 16px">
        <ErrorNotice :error="c.error.value" @retry="c.step.value === 'brief' ? c.findAudience() : c.step.value === 'audience' ? c.generateSegment() : c.generateContent()">
          <button v-if="staleConflict" class="btn" type="button" @click="c.reloadFromServer()">Reload latest</button>
          <a v-if="alreadyApproved && c.campaign.value" class="btn" :href="links.live(c.campaign.value.id)">Open dashboard</a>
          <a v-if="c.error.value.code === 'not_found'" class="btn" :href="links.list()">Back to campaigns</a>
        </ErrorNotice>
      </div>

      <BriefStep
        v-if="c.step.value === 'brief'"
        v-model="c.brief.value"
        :locked="!!c.campaign.value"
        :has-campaign="!!c.campaign.value"
        :busy="c.busy.value === 'create' || c.busy.value === 'segment'"
        @submit="c.findAudience()"
      />

      <AudienceStep
        v-else-if="c.step.value === 'audience' && c.campaign.value?.segment"
        :campaign="c.campaign.value"
        :llm="c.segmentLlm.value"
        :busy="c.busy.value === 'segment'"
        @regenerate="c.generateSegment()"
        @next="c.goTo('content')"
      />

      <ContentStep
        v-else-if="c.step.value === 'content' && c.campaign.value"
        :campaign="c.campaign.value"
        :form="c.form"
        :stream="c.stream"
        :report="c.report.value"
        :report-is-current="c.reportIsCurrent.value"
        :validating="c.validating.value"
        :has-unsaved-edits="c.hasUnsavedEdits.value"
        :saving="c.busy.value === 'save'"
        :llm="c.contentLlm.value"
        @generate="c.generateContent()"
        @stop="c.stopGenerating()"
        @save="c.saveContent()"
        @next="c.goTo('approve')"
      />

      <ApproveStep
        v-else-if="c.step.value === 'approve' && c.campaign.value"
        ref="approveStep"
        :campaign="c.campaign.value"
        :channels="c.channels.value"
        :gate="c.gate.value"
        :recipients="c.recipients.value"
        :approving="c.busy.value === 'approve'"
        @toggle="c.toggleChannel"
        @confirm="confirmApprove"
        @back="c.goTo('content')"
      />
    </template>
  </div>
</template>
