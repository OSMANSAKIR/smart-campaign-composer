<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from './lib/api'
import { links, navNonce, useRoute } from './lib/router'
import CampaignListView from './views/CampaignListView.vue'
import ComposerView from './views/ComposerView.vue'
import DashboardView from './views/DashboardView.vue'
import SystemView from './views/SystemView.vue'

const route = useRoute()
const mock = ref(false)

// The mock-LLM banner is visible on every screen (N-PLAN-004); a failed status call just hides it.
onMounted(async () => {
  try {
    mock.value = (await api.systemStatus()).llm.mock
  } catch {
    mock.value = false
  }
})

const current = computed(() => route.value.name)
function navCurrent(name: string): 'page' | undefined {
  return current.value === name ? 'page' : undefined
}
const viewKey = computed(() => `${navNonce.value}`)
</script>

<template>
  <a class="skip" href="#main" @click.prevent="($refs.main as HTMLElement | undefined)?.focus()">Skip to content</a>
  <header class="topbar">
    <div class="shell">
      <a class="brand" :href="links.list()">Smart Campaign Composer</a>
      <nav aria-label="Main">
        <a :href="links.list()" :aria-current="navCurrent('list')">Campaigns</a>
        <a :href="links.new()" :aria-current="navCurrent('new')">New campaign</a>
        <a :href="links.system()" :aria-current="navCurrent('system')">System</a>
      </nav>
    </div>
  </header>
  <div v-if="mock" class="banner" role="status">
    <div class="shell">Mock LLM — set ANTHROPIC_API_KEY for real generation</div>
  </div>

  <main id="main" ref="main" tabindex="-1" style="outline: none">
    <CampaignListView v-if="route.name === 'list'" :key="viewKey" />
    <ComposerView v-else-if="route.name === 'new'" :key="viewKey" :campaign-id="null" />
    <ComposerView v-else-if="route.name === 'composer'" :key="viewKey" :campaign-id="route.id" />
    <DashboardView v-else-if="route.name === 'live'" :key="viewKey" :campaign-id="route.id" />
    <SystemView v-else-if="route.name === 'system'" :key="viewKey" />
    <div v-else class="shell">
      <div class="empty">
        <p>That page does not exist.</p>
        <a class="btn primary" :href="links.list()">Go to campaigns</a>
      </div>
    </div>
  </main>
</template>
