// The catalog changes only with a deploy, so it is fetched once per page load and shared (api.md §9.1).
import { api } from './api'
import type { Catalog } from './types'

let cached: Promise<Catalog> | null = null

export function loadCatalog(): Promise<Catalog> {
  if (!cached) {
    cached = api.catalog().catch((e) => {
      cached = null // a failed load must be retryable
      throw e
    })
  }
  return cached
}

export function resetCatalogCache() {
  cached = null
}
