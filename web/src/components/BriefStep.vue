<script setup lang="ts">
import { computed } from 'vue'
import { EXAMPLE_BRIEFS } from '../lib/composer'
import { links } from '../lib/router'

const brief = defineModel<string>({ required: true })
const props = defineProps<{ locked: boolean; busy: boolean; hasCampaign: boolean }>()
defineEmits<{ submit: [] }>()

const runes = computed(() => [...brief.value].length)
const valid = computed(() => runes.value >= 1 && runes.value <= 4000)
</script>

<template>
  <section class="card stack" aria-labelledby="brief-h">
    <header>
      <h2 id="brief-h">Describe the campaign</h2>
    </header>
    <label class="field">
      Brief
      <span class="hint">Who should get it, what to say, and the tone. Any language; Claude replies in yours.</span>
      <textarea v-model="brief" rows="5" maxlength="4000" :readonly="locked" aria-describedby="brief-count" />
    </label>
    <p id="brief-count" class="muted num">{{ runes }} / 4000 characters</p>

    <div v-if="!locked">
      <p class="muted" style="margin-bottom: 8px">Or start from an example</p>
      <div class="row">
        <button v-for="ex in EXAMPLE_BRIEFS" :key="ex.label" type="button" class="btn" @click="brief = ex.text">
          {{ ex.label }}
        </button>
      </div>
    </div>
    <p v-else class="muted">
      The brief is fixed once the campaign exists.
      <a :href="links.new()">Start a new campaign</a> to change it.
    </p>

    <div class="row">
      <button class="btn primary" type="button" :disabled="!valid || props.busy" @click="$emit('submit')">
        <span v-if="props.busy" class="spinner" aria-hidden="true" />
        {{ props.busy ? 'Finding audience…' : hasCampaign ? 'Find audience again' : 'Find audience' }}
      </button>
      <span v-if="props.busy" class="muted" role="status">Claude is turning the brief into an audience rule…</span>
    </div>
  </section>
</template>
