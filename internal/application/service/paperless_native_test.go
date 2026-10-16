package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type paperlessNativeKnowledgeRepo struct {
	*createKnowledgeFileRepoStub
	knowledge   *types.Knowledge
	checkParams *types.KnowledgeCheckParams
}

func (r *paperlessNativeKnowledgeRepo) CheckKnowledgeExists(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	params *types.KnowledgeCheckParams,
) (bool, *types.Knowledge, error) {
	copied := *params
	r.checkParams = &copied
	return false, nil, nil
}

func (r *paperlessNativeKnowledgeRepo) CreateKnowledge(ctx context.Context, knowledge *types.Knowledge) error {
	if err := r.createKnowledgeFileRepoStub.CreateKnowledge(ctx, knowledge); err != nil {
		return err
	}
	r.knowledge = knowledge
	return nil
}

func (r *paperlessNativeKnowledgeRepo) GetKnowledgeByID(_ context.Context, tenantID uint64, id string) (*types.Knowledge, error) {
	if r.knowledge == nil || r.knowledge.TenantID != tenantID || r.knowledge.ID != id {
		return nil, fmt.Errorf("knowledge not found: %s", id)
	}
	return r.knowledge, nil
}

func (r *paperlessNativeKnowledgeRepo) UpdateKnowledge(_ context.Context, knowledge *types.Knowledge) error {
	if r.knowledge == nil || knowledge.ID != r.knowledge.ID {
		return fmt.Errorf("knowledge not found: %s", knowledge.ID)
	}
	*r.knowledge = *knowledge
	if r.createdKnowledge != nil {
		copy := *knowledge
		r.createdKnowledge = &copy
	}
	return nil
}

type paperlessNativeFileStore struct {
	interfaces.FileService
	root      string
	savedPath string
	savedData []byte
}

func (s *paperlessNativeFileStore) CheckConnectivity(context.Context) error { return nil }

func (s *paperlessNativeFileStore) SaveFile(_ context.Context, file *multipart.FileHeader, _ uint64, knowledgeID string) (string, error) {
	opened, err := file.Open()
	if err != nil {
		return "", err
	}
	defer opened.Close()
	data, err := io.ReadAll(opened)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(s.root, knowledgeID), 0o700); err != nil {
		return "", err
	}
	filePath := filepath.Join(s.root, knowledgeID, filepath.Base(file.Filename))
	if err := os.WriteFile(filePath, data, 0o600); err != nil {
		return "", err
	}
	s.savedPath = filePath
	s.savedData = append([]byte(nil), data...)
	return filePath, nil
}

func (s *paperlessNativeFileStore) SaveBytes(context.Context, []byte, uint64, string, bool) (string, error) {
	return "", fmt.Errorf("unexpected SaveBytes")
}

func (s *paperlessNativeFileStore) GetFile(_ context.Context, filePath string) (io.ReadCloser, error) {
	return os.Open(filePath)
}

func (s *paperlessNativeFileStore) GetFileURL(context.Context, string) (string, error) {
	return "", fmt.Errorf("unexpected GetFileURL")
}

func (s *paperlessNativeFileStore) DeleteFile(_ context.Context, filePath string) error {
	return os.Remove(filePath)
}

func (s *paperlessNativeFileStore) CopyFile(context.Context, string, uint64, string) (string, error) {
	return "", fmt.Errorf("unexpected CopyFile")
}

type paperlessNativeChunks struct {
	interfaces.ChunkRepository
	created []*types.Chunk
}

func (r *paperlessNativeChunks) DeleteChunksByKnowledgeID(context.Context, uint64, string) error {
	r.created = nil
	return nil
}

func (r *paperlessNativeChunks) CreateChunks(_ context.Context, chunks []*types.Chunk) error {
	r.created = append([]*types.Chunk(nil), chunks...)
	return nil
}

type paperlessNativeTenantRepo struct {
	interfaces.TenantRepository
	tenant *types.Tenant
}

func (r *paperlessNativeTenantRepo) GetTenantByID(_ context.Context, tenantID uint64) (*types.Tenant, error) {
	if r.tenant == nil || r.tenant.ID != tenantID {
		return nil, fmt.Errorf("tenant not found: %d", tenantID)
	}
	return r.tenant, nil
}

func (r *paperlessNativeTenantRepo) AdjustStorageUsed(context.Context, uint64, int64) error {
	return nil
}

type paperlessNativeTenantService struct{ interfaces.TenantService }

func (paperlessNativeTenantService) GetWeKnoraCloudCredentials(context.Context) *types.WeKnoraCloudCredentials {
	return nil
}

type paperlessNativeTaskQueue struct {
	types    []string
	payloads [][]byte
}

func (q *paperlessNativeTaskQueue) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	q.types = append(q.types, task.Type())
	q.payloads = append(q.payloads, append([]byte(nil), task.Payload()...))
	return &asynq.TaskInfo{ID: fmt.Sprintf("native-%d", len(q.types)), Queue: "test", Type: task.Type()}, nil
}

type paperlessNativeEmbedder struct{}

func (paperlessNativeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	return []float32{float32(len(text))}, nil
}

func (paperlessNativeEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, 0, len(texts))
	for _, text := range texts {
		vectors = append(vectors, []float32{float32(len(text))})
	}
	return vectors, nil
}

func (paperlessNativeEmbedder) BatchEmbedWithPool(ctx context.Context, _ embedding.Embedder, texts []string) ([][]float32, error) {
	return (paperlessNativeEmbedder{}).BatchEmbed(ctx, texts)
}

func (paperlessNativeEmbedder) GetModelName() string { return "paperless-native-test" }
func (paperlessNativeEmbedder) GetDimensions() int   { return 1 }
func (paperlessNativeEmbedder) GetModelID() string   { return "paperless-native-test" }

type paperlessNativeModelService struct {
	interfaces.ModelService
	embedder embedding.Embedder
}

func (s paperlessNativeModelService) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	return s.embedder, nil
}

type paperlessNativeRetrieveEngine struct {
	interfaces.RetrieveEngineService
	indexed []*types.IndexInfo
	vectors [][]float32
}

func (*paperlessNativeRetrieveEngine) EngineType() types.RetrieverEngineType {
	return types.PostgresRetrieverEngineType
}

func (*paperlessNativeRetrieveEngine) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType}
}

func (*paperlessNativeRetrieveEngine) DeleteByKnowledgeIDList(context.Context, []string, int, string) error {
	return nil
}

func (*paperlessNativeRetrieveEngine) EstimateStorageSize(context.Context, embedding.Embedder, []*types.IndexInfo, []types.RetrieverType) int64 {
	return 0
}

func (e *paperlessNativeRetrieveEngine) BatchIndex(
	ctx context.Context,
	embedder embedding.Embedder,
	infos []*types.IndexInfo,
	_ []types.RetrieverType,
) error {
	texts := make([]string, 0, len(infos))
	for _, info := range infos {
		texts = append(texts, info.Content)
	}
	vectors, err := embedder.BatchEmbed(ctx, texts)
	if err != nil {
		return err
	}
	if len(vectors) != len(infos) {
		return fmt.Errorf("got %d embeddings for %d index rows", len(vectors), len(infos))
	}
	e.indexed = append([]*types.IndexInfo(nil), infos...)
	e.vectors = vectors
	return nil
}

type paperlessNativeRetrieveRegistry struct {
	interfaces.RetrieveEngineRegistry
	engine interfaces.RetrieveEngineService
}

func (r paperlessNativeRetrieveRegistry) GetRetrieveEngineService(types.RetrieverEngineType) (interfaces.RetrieveEngineService, error) {
	return r.engine, nil
}

func TestPaperlessOriginalUsesNativeKnowledgeParserChunkerAndIndexPath(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,::1")
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	t.Setenv("RETRIEVE_DRIVER", "")

	original := []byte("# Annual report\n\nThe original Paperless text has revenue, expenses, and operating notes.\nA second paragraph is retained for native chunking.\n")
	manualService, sources, bases, _, server := paperlessManualServiceFixture(t, paperlessManualImportResponse(t, original, "annual-report.txt", 0))
	defer server.Close()

	kb := &types.KnowledgeBase{
		ID: "kb-7", TenantID: 7, EmbeddingModelID: "test-embedding",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
	}
	bases.kb = kb
	tenant := &types.Tenant{
		ID: 7,
		RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{{
			RetrieverType: types.VectorRetrieverType, RetrieverEngineType: types.PostgresRetrieverEngineType,
		}}},
	}
	ctx := paperlessManualContext(7, types.TenantRoleAdmin)
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)

	repo := &paperlessNativeKnowledgeRepo{createKnowledgeFileRepoStub: &createKnowledgeFileRepoStub{}}
	fileStore := &paperlessNativeFileStore{root: t.TempDir()}
	chunkRepo := &paperlessNativeChunks{}
	retrieveEngine := &paperlessNativeRetrieveEngine{}
	queue := &paperlessNativeTaskQueue{}
	nativeKnowledge := &knowledgeService{
		repo: repo, kbService: &createKnowledgeFileKBServiceStub{kb: kb},
		fileSvc: fileStore, task: queue, tenantService: paperlessNativeTenantService{},
		tenantRepo: &paperlessNativeTenantRepo{tenant: tenant},
		chunkRepo:  chunkRepo, graphEngine: parentChildGraphRepo{},
		modelService:   paperlessNativeModelService{embedder: paperlessNativeEmbedder{}},
		retrieveEngine: paperlessNativeRetrieveRegistry{engine: retrieveEngine},
	}
	manualService.knowledge = nativeKnowledge

	response, err := manualService.Import(ctx, sources.ds.ID, PaperlessImportRequest{
		DocumentIDs: []int{42}, FolderPath: "Paperless/Acceptance",
	})
	require.NoError(t, err)
	require.Len(t, response.Results, 1)
	require.Empty(t, response.Results[0].Error)
	require.NotEmpty(t, response.Results[0].KnowledgeID)
	require.Equal(t, 1, repo.createCalls)
	require.NotNil(t, repo.createdKnowledge)
	require.NotNil(t, repo.checkParams)

	knowledge := repo.knowledge
	require.Equal(t, response.Results[0].KnowledgeID, knowledge.ID)
	require.Equal(t, uint64(7), knowledge.TenantID)
	require.Equal(t, "kb-7", knowledge.KnowledgeBaseID)
	require.Equal(t, "annual-report.txt", knowledge.FileName)
	require.Equal(t, "Paperless/Acceptance", knowledge.FolderPath)
	require.Equal(t, int64(len(original)), knowledge.FileSize)
	require.Equal(t, "file", knowledge.Type)
	require.Equal(t, types.ConnectorTypePaperless, knowledge.Channel)
	require.Equal(t, md5Hex(string(original)), knowledge.FileHash)
	require.Equal(t, md5Hex(string(original)), repo.checkParams.FileHash)
	require.Equal(t, int64(len(original)), repo.checkParams.FileSize)
	require.Equal(t, fileStore.savedPath, knowledge.FilePath)
	require.Equal(t, original, fileStore.savedData)
	stored, err := os.ReadFile(fileStore.savedPath)
	require.NoError(t, err)
	require.Equal(t, original, stored, "native FileService persisted the exact original in a temporary directory")
	metadata, err := knowledge.Metadata.Map()
	require.NoError(t, err)
	require.Equal(t, "paperless", metadata["source"])
	require.Equal(t, "42", metadata["paperless_document_id"])
	require.Equal(t, sources.ds.ID, metadata["paperless_connection_id"])
	require.Equal(t, "annual-report.txt", metadata["original_filename"])

	// The first queued task is the native DocumentProcess task created by
	// CreateKnowledgeFromFile. Its payload points at the isolated saved file.
	require.NotEmpty(t, queue.types)
	require.Equal(t, types.TypeDocumentProcess, queue.types[0])
	var processPayload types.DocumentProcessPayload
	require.NoError(t, json.Unmarshal(queue.payloads[0], &processPayload))
	require.Equal(t, knowledge.ID, processPayload.KnowledgeID)
	require.Equal(t, fileStore.savedPath, processPayload.FilePath)
	require.Equal(t, "annual-report.txt", processPayload.FileName)

	// Run the real ProcessDocument path. A .txt uses the repository's native
	// Go simple-format reader and Go chunker; the existing deterministic test
	// embedder/retrieve-engine fixtures keep embedding and vector-store effects
	// local and in memory.
	require.NoError(t, nativeKnowledge.ProcessDocument(ctx, asynq.NewTask(types.TypeDocumentProcess, queue.payloads[0])))
	require.NotEmpty(t, chunkRepo.created)
	var textChunks []*types.Chunk
	for _, chunk := range chunkRepo.created {
		if chunk.ChunkType == types.ChunkTypeText {
			textChunks = append(textChunks, chunk)
		}
	}
	require.NotEmpty(t, textChunks)
	require.Contains(t, strings.Join(chunkContents(textChunks), "\n"), "revenue, expenses")
	require.Len(t, retrieveEngine.indexed, len(textChunks))
	for _, info := range retrieveEngine.indexed {
		require.Equal(t, knowledge.ID, info.KnowledgeID)
		require.Equal(t, "kb-7", info.KnowledgeBaseID)
		require.Equal(t, types.ChunkSourceType, info.SourceType)
	}
	require.Len(t, retrieveEngine.vectors, len(retrieveEngine.indexed), "deterministic local embedder ran inside the native BatchIndex path")
	require.Equal(t, types.ParseStatusProcessing, repo.knowledge.ParseStatus, "native post-process task remains queued for its own enrichment stage")
	require.Contains(t, queue.types, types.TypeKnowledgePostProcess)
	require.Equal(t, 0, sources.mutationCalls, "one-time import does not mutate datasource filters or create a sync subscription")
}

func chunkContents(chunks []*types.Chunk) []string {
	contents := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		contents = append(contents, chunk.Content)
	}
	return contents
}

var _ interfaces.FileService = (*paperlessNativeFileStore)(nil)
