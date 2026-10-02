export const PAPERLESS_PAGE_SIZE = 20
export const PAPERLESS_IMPORT_MAX = 100

export function buildPaperlessFilters(input: {
  query: string
  tags: number[]
  documentType: number | ''
  correspondent: number | ''
  page: number
}) {
  return {
    query: input.query.trim() || undefined,
    tags: input.tags.length ? [...input.tags] : undefined,
    document_type: input.documentType === '' ? undefined : input.documentType,
    correspondent: input.correspondent === '' ? undefined : input.correspondent,
    page: input.page,
    page_size: PAPERLESS_PAGE_SIZE,
  }
}

export function resetPaperlessImportState(state: {
  connectionId: string
  query: string
  tags: number[]
  documentType: number | ''
  correspondent: number | ''
  page: number
  selectedIds: Set<number>
}) {
  state.connectionId = ''
  state.query = ''
  state.tags = []
  state.documentType = ''
  state.correspondent = ''
  state.page = 1
  state.selectedIds.clear()
}

export function selectPaperlessPage(
  selected: Set<number>,
  pageIds: number[],
  checked: boolean,
  max = PAPERLESS_IMPORT_MAX,
) {
  if (!checked) {
    pageIds.forEach((id) => selected.delete(id))
    return 0
  }
  let overflow = 0
  for (const id of pageIds) {
    if (selected.has(id)) continue
    if (selected.size >= max) {
      overflow++
      continue
    }
    selected.add(id)
  }
  return overflow
}

export function replacePaperlessPageSelection(
  selected: Set<number>,
  pageIds: number[],
  checkedIds: number[],
  max = PAPERLESS_IMPORT_MAX,
) {
  const checked = new Set(checkedIds)
  pageIds.forEach((id) => {
    if (!checked.has(id)) selected.delete(id)
  })
  return selectPaperlessPage(
    selected,
    pageIds.filter((id) => checked.has(id)),
    true,
    max,
  )
}
