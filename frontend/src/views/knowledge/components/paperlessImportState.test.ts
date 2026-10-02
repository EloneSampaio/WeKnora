import test from 'node:test'
import assert from 'node:assert/strict'
import {
  buildPaperlessFilters,
  resetPaperlessImportState,
  replacePaperlessPageSelection,
  selectPaperlessPage,
} from './paperlessImportState.ts'

test('Paperless filters trim search and preserve AND tag ids with independent metadata filters', () => {
  assert.deepEqual(
    buildPaperlessFilters({
      query: ' invoice ',
      tags: [2, 9],
      documentType: 3,
      correspondent: '',
      page: 2,
    }),
    {
      query: 'invoice',
      tags: [2, 9],
      document_type: 3,
      correspondent: undefined,
      page: 2,
      page_size: 20,
    },
  )
})

test('closing and reopening starts with a clean browsing and cross-page selection state', () => {
  const state = {
    connectionId: 'old',
    query: 'x',
    tags: [2],
    documentType: 3 as number | '',
    correspondent: 4 as number | '',
    page: 5,
    selectedIds: new Set([10, 11]),
  }
  resetPaperlessImportState(state)
  assert.deepEqual(state, {
    connectionId: '',
    query: '',
    tags: [],
    documentType: '',
    correspondent: '',
    page: 1,
    selectedIds: new Set(),
  })
})

test('selecting a page is idempotent and deselecting it preserves selections from other pages', () => {
  const selected = new Set([1, 4])
  assert.equal(selectPaperlessPage(selected, [2, 3], true), 0)
  assert.equal(selectPaperlessPage(selected, [2, 3], true), 0)
  assert.deepEqual([...selected].sort(), [1, 2, 3, 4])
  selectPaperlessPage(selected, [2, 3], false)
  assert.deepEqual([...selected], [1, 4])
})

test('replacing page selection preserves other pages and reports max-selection overflow', () => {
  const selected = new Set([1, 2, 3])
  assert.equal(replacePaperlessPageSelection(selected, [2, 3, 4], [3, 4], 3), 0)
  assert.deepEqual([...selected], [1, 3, 4])
  const overflow = replacePaperlessPageSelection(selected, [5, 6], [5, 6], 3)
  assert.equal(overflow, 2)
  assert.deepEqual([...selected], [1, 3, 4])
})

// Audit actual template keys absent from every locale; parity cannot detect these.
test('Paperless dialog literal translation keys exist in the English baseline', async () => {
  const { readFileSync } = await import('node:fs')
  const { default: messages } = await import('../../../i18n/locales/en-US.ts')
  const source = readFileSync(new URL('./PaperlessImportDialog.vue', import.meta.url), 'utf8')
  for (const match of source.matchAll(/\bt\('([^']+)'/g)) {
    const resolved = match[1].split('.').reduce((value: any, part) => value?.[part], messages)
    assert.equal(typeof resolved, 'string', match[1])
  }
})
