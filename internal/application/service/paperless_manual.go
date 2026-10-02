package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/datasource/connector/paperless"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

// validateManualDataSource makes the connection-only contract explicit using
// the existing schema. Empty/default modes keep their existing sync semantics.
func validateManualDataSource(ds *types.DataSource) error {
	if ds.SyncMode != types.SyncModeManual {
		return nil
	}
	if ds.Type != types.ConnectorTypePaperless {
		return fmt.Errorf("manual mode is supported only for Paperless")
	}
	cfg, err := ds.ParseConfig()
	if err != nil || cfg == nil {
		return fmt.Errorf("invalid manual connection configuration")
	}
	if ds.SyncSchedule != "" || len(cfg.ResourceIDs) != 0 || len(cfg.Settings) != 0 {
		// base_url is configuration, not a selection/filter rule.
		if ds.SyncSchedule != "" || len(cfg.ResourceIDs) != 0 {
			return fmt.Errorf("manual connections cannot store schedules or resource selections")
		}
		for key := range cfg.Settings {
			if key != "base_url" {
				return fmt.Errorf("manual connections cannot store filter settings")
			}
		}
	}
	ds.Status = types.DataSourceStatusPaused
	ds.SyncDeletions = false
	return nil
}

// PaperlessManualService complements sync without creating sync logs, cursors,
// resource selections, or bindings. Every imported file enters native ingestion.
type PaperlessManualService struct {
	sources   interfaces.DataSourceService
	bases     interfaces.KnowledgeBaseService
	knowledge interfaces.KnowledgeService
}

func NewPaperlessManualService(sources interfaces.DataSourceService, bases interfaces.KnowledgeBaseService, knowledge interfaces.KnowledgeService) *PaperlessManualService {
	return &PaperlessManualService{sources: sources, bases: bases, knowledge: knowledge}
}

func (s *PaperlessManualService) connection(ctx context.Context, id string, write bool) (*types.DataSource, *paperless.ManualClient, context.Context, error) {
	ds, err := s.sources.GetDataSource(ctx, id)
	if err != nil || ds == nil {
		return nil, nil, ctx, access.ErrNotFound
	}
	caller := types.CallerFromContext(ctx)
	if caller.TenantID == 0 {
		return nil, nil, ctx, access.ErrUnauthorized
	}
	if !caller.Role.HasPermission(types.TenantRoleAdmin) {
		return nil, nil, ctx, access.ErrForbidden
	}
	if scope, ok := types.TenantAPIKeyScopeFromContext(ctx); ok && !scope.FullAccess {
		if !scope.HasCapability(types.APIKeyCapabilityManageDataSources) || (write && !scope.HasCapability(types.APIKeyCapabilityIngest)) {
			return nil, nil, ctx, access.ErrForbidden
		}
	}
	// Credentials are never reusable across tenants, including shared KBs.
	if ds.TenantID != caller.TenantID {
		return nil, nil, ctx, access.ErrForbidden
	}
	kb, err := s.bases.GetKnowledgeBaseByID(ctx, ds.KnowledgeBaseID)
	if err != nil || kb == nil {
		return nil, nil, ctx, access.ErrNotFound
	}
	if kb.TenantID != caller.TenantID {
		return nil, nil, ctx, access.ErrForbidden
	}
	required := types.OrgRoleViewer
	if write {
		required = types.OrgRoleEditor
	}
	grant, err := access.ResolveKB(ctx, access.KBRequest{Caller: caller}, kb, required, nil, nil)
	if err != nil {
		return nil, nil, ctx, err
	}
	if ds.Type != types.ConnectorTypePaperless {
		return nil, nil, ctx, fmt.Errorf("not a Paperless connection")
	}
	cfg, err := ds.ParseConfig()
	if err != nil {
		return nil, nil, ctx, err
	}
	client, err := paperless.NewManualClient(cfg)
	return ds, client, grant.Context(ctx), err
}

func (s *PaperlessManualService) Facets(ctx context.Context, id string) (*paperless.ManualFacets, error) {
	_, client, ctx, err := s.connection(ctx, id, false)
	if err != nil {
		return nil, err
	}
	return client.Facets(ctx)
}

func (s *PaperlessManualService) Documents(ctx context.Context, id string, filter paperless.ManualDocumentFilter) (*paperless.ManualDocumentsPage, error) {
	_, client, ctx, err := s.connection(ctx, id, false)
	if err != nil {
		return nil, err
	}
	return client.Documents(ctx, filter)
}

type PaperlessImportRequest struct {
	DocumentIDs []int  `json:"document_ids"`
	FolderPath  string `json:"folder_path"`
}

type PaperlessImportResult struct {
	DocumentID  int    `json:"document_id"`
	KnowledgeID string `json:"knowledge_id,omitempty"`
	Duplicate   bool   `json:"duplicate,omitempty"`
	Error       string `json:"error,omitempty"`
}

type PaperlessImportResponse struct {
	Results []PaperlessImportResult `json:"results"`
}

func (s *PaperlessManualService) Import(ctx context.Context, id string, req PaperlessImportRequest) (*PaperlessImportResponse, error) {
	if len(req.DocumentIDs) == 0 || len(req.DocumentIDs) > 100 {
		return nil, fmt.Errorf("select between 1 and 100 documents")
	}
	for _, docID := range req.DocumentIDs {
		if docID <= 0 {
			return nil, fmt.Errorf("document IDs must be positive")
		}
	}
	ds, client, ctx, err := s.connection(ctx, id, true)
	if err != nil {
		return nil, err
	}
	folder := strings.TrimSpace(req.FolderPath)
	if folder != "" {
		normalized, _ := types.SplitKnowledgeRelativePath(folder + "/document.pdf")
		if normalized != folder {
			return nil, fmt.Errorf("invalid folder path")
		}
		if _, valid := utils.ValidateInput(folder); !valid {
			return nil, fmt.Errorf("invalid folder path")
		}
	}
	cfg, _ := ds.ParseConfig()
	baseURL, _ := cfg.Settings["base_url"].(string)
	if baseURL == "" {
		baseURL, _ = cfg.Credentials["base_url"].(string)
	}
	response := &PaperlessImportResponse{Results: make([]PaperlessImportResult, 0, len(req.DocumentIDs))}
	seen := map[int]bool{}
	for _, docID := range req.DocumentIDs {
		if seen[docID] {
			continue
		}
		seen[docID] = true
		result := PaperlessImportResult{DocumentID: docID}
		original, err := client.Original(ctx, docID, utils.GetMaxFileSize())
		if err != nil {
			result.Error = err.Error()
		} else {
			file, buildErr := bytesToFileHeader(original.Data, original.Filename)
			if buildErr != nil {
				result.Error = "could not prepare original file"
			} else if validationErr := ValidateJSONUploadContent(original.Filename, file); validationErr != nil {
				result.Error = validationErr.Error()
			} else {
				filename := original.Filename
				if folder != "" {
					filename = path.Join(folder, filename)
				}
				metadata := map[string]string{"source": "paperless", "paperless_document_id": strconv.Itoa(docID), "paperless_connection_id": ds.ID, "source_url": strings.TrimRight(baseURL, "/") + "/documents/" + strconv.Itoa(docID) + "/", "original_filename": original.Filename}
				knowledge, ingestErr := s.knowledge.CreateKnowledgeFromFile(ctx, ds.KnowledgeBaseID, file, metadata, nil, filename, nil, types.ConnectorTypePaperless, nil)
				var duplicate *types.DuplicateKnowledgeError
				if errors.As(ingestErr, &duplicate) {
					result.Duplicate = true
				} else if ingestErr != nil {
					result.Error = ingestErr.Error()
				}
				if knowledge != nil {
					result.KnowledgeID = knowledge.ID
				}
			}
		}
		response.Results = append(response.Results, result)
		if ctx.Err() != nil {
			break
		}
	}
	return response, nil
}
