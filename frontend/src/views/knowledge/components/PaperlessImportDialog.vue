<template>
  <t-dialog
    v-model:visible="visible"
    :header="t('datasource.paperless.importTitle')"
    width="880px"
    :confirm-btn="null"
    :cancel-btn="null"
    @close="close"
  >
    <div class="paperless-import">
      <div class="connection-row">
        <t-select
          v-model="connectionId"
          :options="connectionOptions"
          :placeholder="t('datasource.paperless.selectConnection')"
          :loading="loadingConnections"
          :disabled="importing"
          @change="onConnectionChange"
        />
        <t-button
          variant="text"
          :disabled="importing || savingConnection"
          @click="creatingConnection = !creatingConnection"
          >{{
            t(creatingConnection ? 'common.cancel' : 'datasource.paperless.newConnection')
          }}</t-button
        >
      </div>
      <div v-if="creatingConnection" class="connection-form">
        <t-input
          v-model.trim="connectionName"
          :disabled="savingConnection || importing"
          :placeholder="t('datasource.nameLabel')"
        />
        <t-input
          v-model.trim="baseUrl"
          :disabled="savingConnection || importing"
          :placeholder="t('datasource.paperless.baseUrl')"
        />
        <t-input
          v-model="apiToken"
          type="password"
          :disabled="savingConnection || importing"
          :placeholder="t('datasource.paperless.apiToken')"
        />
        <t-button theme="primary" :loading="savingConnection" @click="createConnection">{{
          t('common.save')
        }}</t-button>
      </div>
      <template v-if="connectionId">
        <div class="filters">
          <t-input
            v-model="query"
            clearable
            :disabled="importing"
            :placeholder="t('datasource.paperless.search')"
            @enter="applyFilters"
            @clear="applyFilters"
          />
          <t-select
            v-model="tags"
            multiple
            clearable
            :disabled="importing"
            :options="tagOptions"
            :placeholder="t('datasource.paperless.tags')"
            @change="applyFilters"
          />
          <t-select
            v-model="documentType"
            clearable
            :disabled="importing"
            :options="typeOptions"
            :placeholder="t('datasource.paperless.documentTypes')"
            @change="applyFilters"
          />
          <t-select
            v-model="correspondent"
            clearable
            :disabled="importing"
            :options="correspondentOptions"
            :placeholder="t('datasource.paperless.correspondents')"
            @change="applyFilters"
          />
          <t-button :disabled="importing" @click="applyFilters">{{ t('menu.search') }}</t-button>
        </div>
        <div class="selection-summary">
          <span>{{ t('datasource.paperless.selectedCount', { count: selectedIds.size }) }}</span>
          <t-button
            variant="text"
            :disabled="!documents.length || importing"
            @click="selectCurrentPage"
            >{{ t('datasource.paperless.selectPage') }}</t-button
          >
          <t-button
            variant="text"
            :disabled="!selectedIds.size || importing"
            @click="selectedIds.clear()"
            >{{ t('datasource.paperless.clearSelection') }}</t-button
          >
          <t-button
            theme="primary"
            :disabled="!selectedIds.size || importing"
            :loading="importing"
            @click="runImport"
            >{{ t('datasource.paperless.importSelected') }}</t-button
          >
        </div>
        <t-loading :loading="loadingDocuments || importing" show-overlay>
          <t-table
            :max-height="360"
            row-key="id"
            :data="documents"
            :columns="columns"
            :selected-row-keys="[...selectedIds]"
            :pagination="pagination"
            @select-change="onSelectChange"
            @page-change="onPageChange"
          />
        </t-loading>
        <div v-if="importResults.length" class="import-results" role="status">
          <div
            v-for="result in importResults"
            :key="result.document_id"
            :class="{ failed: result.error }"
          >
            {{ documentName(result.document_id) }} —
            {{
              result.error ||
              (result.duplicate
                ? t('datasource.paperless.duplicate')
                : t('datasource.paperless.imported'))
            }}
          </div>
        </div>
      </template>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, toRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import {
  createDataSource,
  getPaperlessFacets,
  importPaperlessDocuments,
  listDataSources,
  listPaperlessDocuments,
  validateCredentials,
  type DataSource,
  type PaperlessDocument,
  type PaperlessDocumentPage,
  type PaperlessFacet,
  type PaperlessImportResult,
} from '@/api/datasource'
import {
  buildPaperlessFilters,
  PAPERLESS_IMPORT_MAX,
  PAPERLESS_PAGE_SIZE,
  replacePaperlessPageSelection,
  resetPaperlessImportState,
  selectPaperlessPage,
} from './paperlessImportState'

const props = defineProps<{ kbId: string; folderPath?: string }>()
const emit = defineEmits<{ imported: [] }>()
const visible = defineModel<boolean>('visible', { default: false })
const { t } = useI18n()
const session = reactive({
  connectionId: '',
  query: '',
  tags: [] as number[],
  documentType: '' as number | '',
  correspondent: '' as number | '',
  page: 1,
  selectedIds: new Set<number>(),
})
const connectionId = toRef(session, 'connectionId')
const query = toRef(session, 'query')
const tags = toRef(session, 'tags')
const documentType = toRef(session, 'documentType')
const correspondent = toRef(session, 'correspondent')
const page = toRef(session, 'page')
const selectedIds = toRef(session, 'selectedIds')
const connections = ref<DataSource[]>([])
const loadingConnections = ref(false)
const creatingConnection = ref(false)
const savingConnection = ref(false)
const connectionName = ref('')
const baseUrl = ref('')
const apiToken = ref('')
const documents = ref<PaperlessDocument[]>([])
const facets = ref<{
  tags: PaperlessFacet[]
  document_types: PaperlessFacet[]
  correspondents: PaperlessFacet[]
}>({ tags: [], document_types: [], correspondents: [] })
const loadingDocuments = ref(false)
const importing = ref(false)
const results = ref<PaperlessDocumentPage>({
  count: 0,
  results: [],
  page: 1,
  page_size: PAPERLESS_PAGE_SIZE,
  has_next: false,
})
const importResults = ref<PaperlessImportResult[]>([])
let browseEpoch = 0
let sessionEpoch = 0
let connectionLoadEpoch = 0
const connectionOptions = computed(() =>
  connections.value.map((ds) => ({ label: ds.name, value: ds.id })),
)
const options = (items: PaperlessFacet[] | null | undefined) =>
  (items || []).map((item) => ({ label: item.name, value: item.id }))
const tagOptions = computed(() => options(facets.value.tags))
const typeOptions = computed(() => options(facets.value.document_types))
const correspondentOptions = computed(() => options(facets.value.correspondents))
const columns = computed(() => [
  { colKey: 'row-select', type: 'multiple' },
  { colKey: 'title', title: t('datasource.paperless.title'), ellipsis: true },
  { colKey: 'original_file_name', title: t('datasource.paperless.fileName'), ellipsis: true },
])
const pagination = computed(() => ({
  current: page.value,
  pageSize: PAPERLESS_PAGE_SIZE,
  total: results.value.count,
  showPageSize: false,
}))
const documentName = (id: number) =>
  documents.value.find((doc) => doc.id === id)?.title || String(id)

async function loadConnections() {
  const epoch = ++connectionLoadEpoch
  loadingConnections.value = true
  try {
    const response: any = await listDataSources(props.kbId)
    const data = response?.data || response || []
    if (epoch === connectionLoadEpoch && visible.value)
      connections.value = data.filter(
        (item: DataSource) =>
          item.type === 'paperless' && item.credentials?.credentials?.configured,
      )
  } catch (e: any) {
    if (epoch === connectionLoadEpoch && visible.value)
      MessagePlugin.error(e?.message || t('datasource.paperless.loadFailed'))
  } finally {
    if (epoch === connectionLoadEpoch) loadingConnections.value = false
  }
}
async function onConnectionChange(id: string) {
  resetBrowse()
  if (!id) return
  const epoch = ++browseEpoch
  try {
    const response: any = await getPaperlessFacets(id)
    if (epoch !== browseEpoch || !visible.value) return
    const data = response?.data || response || {}
    facets.value = {
      tags: data.tags || [],
      document_types: data.document_types || [],
      correspondents: data.correspondents || [],
    }
    await loadDocuments()
  } catch (e: any) {
    if (epoch === browseEpoch)
      MessagePlugin.error(e?.message || t('datasource.paperless.loadFailed'))
  }
}
function resetBrowse() {
  ++browseEpoch
  documents.value = []
  facets.value = { tags: [], document_types: [], correspondents: [] }
  query.value = ''
  tags.value = []
  documentType.value = ''
  correspondent.value = ''
  page.value = 1
  selectedIds.value.clear()
  importResults.value = []
}
async function loadDocuments() {
  const id = connectionId.value
  if (!id) return
  const epoch = ++browseEpoch
  loadingDocuments.value = true
  try {
    const params = buildPaperlessFilters({
      query: query.value,
      tags: tags.value,
      documentType: documentType.value,
      correspondent: correspondent.value,
      page: page.value,
    })
    const response: any = await listPaperlessDocuments(id, params)
    if (epoch !== browseEpoch || id !== connectionId.value || !visible.value) return
    const data = response?.data || response
    results.value = data
    documents.value = data.results || []
  } catch (e: any) {
    if (epoch === browseEpoch)
      MessagePlugin.error(e?.message || t('datasource.paperless.loadFailed'))
  } finally {
    if (epoch === browseEpoch) loadingDocuments.value = false
  }
}
function applyFilters() {
  page.value = 1
  void loadDocuments()
}
function onPageChange(info: { current: number }) {
  if (importing.value) return
  page.value = info.current
  void loadDocuments()
}
function onSelectChange(keys: Array<string | number>) {
  if (importing.value) return
  const overflow = replacePaperlessPageSelection(
    selectedIds.value,
    documents.value.map((doc) => doc.id),
    keys.map(Number),
  )
  if (overflow)
    MessagePlugin.warning(t('datasource.paperless.selectionLimit', { max: PAPERLESS_IMPORT_MAX }))
}
function selectCurrentPage() {
  if (importing.value) return
  const ids = documents.value.map((doc) => doc.id)
  const overflow = selectPaperlessPage(selectedIds.value, ids, true)
  if (overflow)
    MessagePlugin.warning(t('datasource.paperless.selectionLimit', { max: PAPERLESS_IMPORT_MAX }))
}
async function createConnection() {
  if (importing.value || savingConnection.value) return
  if (!connectionName.value || !baseUrl.value || !apiToken.value) {
    MessagePlugin.warning(t('datasource.paperless.credentialsRequired'))
    return
  }
  const requestEpoch = sessionEpoch
  const name = connectionName.value
  const url = baseUrl.value
  const token = apiToken.value
  savingConnection.value = true
  try {
    await validateCredentials('paperless', { base_url: url, api_token: token })
    if (requestEpoch !== sessionEpoch || !visible.value) return
    const response: any = await createDataSource({
      name,
      type: 'paperless',
      knowledge_base_id: props.kbId,
      status: 'paused',
      sync_mode: 'manual' as any,
      sync_schedule: '',
      sync_deletions: false,
      config: { resource_ids: [], settings: { base_url: url }, credentials: { api_token: token } },
    } as any)
    if (requestEpoch !== sessionEpoch || !visible.value) return
    const created: DataSource = response?.data || response
    connections.value.push(created)
    creatingConnection.value = false
    connectionName.value = ''
    baseUrl.value = ''
    apiToken.value = ''
    connectionId.value = created.id
    await onConnectionChange(created.id)
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('datasource.paperless.connectionFailed'))
  } finally {
    if (requestEpoch === sessionEpoch) savingConnection.value = false
  }
}
async function runImport() {
  if (!selectedIds.value.size || importing.value) return
  if (selectedIds.value.size > PAPERLESS_IMPORT_MAX) {
    MessagePlugin.warning(t('datasource.paperless.selectionLimit', { max: PAPERLESS_IMPORT_MAX }))
    return
  }
  const requestEpoch = browseEpoch
  const requestSessionEpoch = sessionEpoch
  importing.value = true
  try {
    const response: any = await importPaperlessDocuments(
      connectionId.value,
      [...selectedIds.value],
      props.folderPath,
    )
    const data = response?.data || response
    const receivedResults: PaperlessImportResult[] = data.results || []
    if (receivedResults.some((item) => !item.error && !item.duplicate)) emit('imported')
    if (requestEpoch !== browseEpoch || requestSessionEpoch !== sessionEpoch) return
    importResults.value = receivedResults
    const failures = receivedResults.filter((item) => item.error).length
    if (!failures) MessagePlugin.success(t('datasource.paperless.importFinished'))
    else
      MessagePlugin.warning(
        t('datasource.paperless.importPartial', {
          failed: failures,
          total: importResults.value.length,
        }),
      )
    const done = new Set(
      receivedResults.filter((item) => !item.error).map((item) => item.document_id),
    )
    done.forEach((id) => selectedIds.value.delete(id))
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('datasource.paperless.importFailed'))
  } finally {
    if (requestSessionEpoch === sessionEpoch) importing.value = false
  }
}
function resetSession() {
  ++sessionEpoch
  ++connectionLoadEpoch
  resetPaperlessImportState(session)
  documents.value = []
  results.value = {
    count: 0,
    results: [],
    page: 1,
    page_size: PAPERLESS_PAGE_SIZE,
    has_next: false,
  }
  facets.value = { tags: [], document_types: [], correspondents: [] }
  importResults.value = []
  creatingConnection.value = false
  savingConnection.value = false
  importing.value = false
  connectionName.value = ''
  baseUrl.value = ''
  apiToken.value = ''
}
function close() {
  ++browseEpoch
  resetSession()
  visible.value = false
}
watch(visible, async (open) => {
  if (open) {
    resetSession()
    await loadConnections()
  } else {
    ++browseEpoch
    resetSession()
  }
})
</script>

<style scoped>
.paperless-import {
  display: grid;
  gap: 14px;
}
.connection-row {
  display: flex;
  gap: 10px;
  align-items: center;
}
.connection-row :deep(.t-select) {
  flex: 1;
}
.connection-form {
  display: grid;
  grid-template-columns: 1fr 1.2fr 1fr auto;
  gap: 8px;
  padding: 12px;
  background: var(--td-bg-color-container-hover);
  border-radius: 8px;
}
.filters {
  display: grid;
  grid-template-columns: 1.2fr 1.2fr 1fr 1fr auto;
  gap: 8px;
}
.selection-summary {
  display: flex;
  align-items: center;
  gap: 8px;
}
.selection-summary span {
  margin-right: auto;
  color: var(--td-text-color-secondary);
}
.import-results {
  max-height: 120px;
  overflow: auto;
  font-size: 12px;
  color: var(--td-text-color-secondary);
}
.import-results .failed {
  color: var(--td-error-color);
}
@media (max-width: 700px) {
  .connection-form,
  .filters {
    grid-template-columns: 1fr 1fr;
  }
}
</style>
