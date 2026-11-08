// Minimal hash router. Hash URLs need no server-side fallback, so the Go API can serve the SPA as plain files.
import { computed, onBeforeUnmount, ref } from 'vue'

export type Route =
  | { name: 'list' }
  | { name: 'new' }
  | { name: 'composer'; id: string }
  | { name: 'live'; id: string }
  | { name: 'system' }
  | { name: 'notfound' }

export function parseHash(hash: string): Route {
  const path = hash.replace(/^#/, '').split('?')[0] ?? ''
  const parts = path.split('/').filter(Boolean)
  if (parts.length === 0) return { name: 'list' }
  if (parts[0] === 'new' && parts.length === 1) return { name: 'new' }
  if (parts[0] === 'system' && parts.length === 1) return { name: 'system' }
  if (parts[0] === 'c' && parts[1]) {
    const id = decodeURIComponent(parts[1])
    if (parts.length === 2) return { name: 'composer', id }
    if (parts.length === 3 && parts[2] === 'live') return { name: 'live', id }
  }
  return { name: 'notfound' }
}

export const links = {
  list: () => '#/',
  new: () => '#/new',
  system: () => '#/system',
  composer: (id: string) => `#/c/${encodeURIComponent(id)}`,
  live: (id: string) => `#/c/${encodeURIComponent(id)}/live`,
}

export function navigate(hash: string) {
  window.location.hash = hash
}

const hash = ref(typeof window === 'undefined' ? '' : window.location.hash)
// Bumped on every user-driven navigation so views remount even when the route name is unchanged
// (for example "New campaign" clicked while a just-created campaign is open at the same screen).
export const navNonce = ref(0)

// Updates the address bar without remounting the current view (used once a new campaign gets its id).
export function replaceHash(h: string) {
  history.replaceState(null, '', h)
  hash.value = h
}

export function useRoute() {
  const sync = () => {
    hash.value = window.location.hash
    navNonce.value++
  }
  window.addEventListener('hashchange', sync)
  onBeforeUnmount(() => window.removeEventListener('hashchange', sync))
  return computed(() => parseHash(hash.value))
}
