package paperless

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	manualDefaultPageSize = 25
	manualMaxPageSize     = 100
)

// ManualClient provides bounded, read-only Paperless operations for manual
// document selection. The existing datasource connector remains the owner of
// synchronization behavior.
type ManualClient struct {
	client *client
}

// ManualFacetOption is a selectable Paperless tag, document type, or
// correspondent.
type ManualFacetOption struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// ManualFacets contains the complete paginated selector vocabulary.
type ManualFacets struct {
	Tags           []ManualFacetOption `json:"tags"`
	DocumentTypes  []ManualFacetOption `json:"document_types"`
	Correspondents []ManualFacetOption `json:"correspondents"`
}

// ManualDocumentFilter selects one bounded page of documents. Query uses
// Paperless full-text search; Tags are combined with AND semantics.
type ManualDocumentFilter struct {
	Query           string `json:"query,omitempty"`
	Tags            []int  `json:"tags,omitempty"`
	DocumentTypeID  int    `json:"document_type_id,omitempty"`
	CorrespondentID int    `json:"correspondent_id,omitempty"`
	Page            int    `json:"page,omitempty"`
	PageSize        int    `json:"page_size,omitempty"`
}

// ManualDocument is the selector-safe subset of Paperless document metadata.
type ManualDocument struct {
	ID               int    `json:"id"`
	Title            string `json:"title"`
	OriginalFileName string `json:"original_file_name,omitempty"`
	Tags             []int  `json:"tags,omitempty"`
	DocumentTypeID   int    `json:"document_type_id,omitempty"`
	CorrespondentID  int    `json:"correspondent_id,omitempty"`
}

// ManualDocumentsPage reports one bounded page and Paperless's total count.
type ManualDocumentsPage struct {
	Count    int              `json:"count"`
	Results  []ManualDocument `json:"results"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
	HasNext  bool             `json:"has_next"`
}

// ManualOriginal is one Paperless original file and its safe display name.
type ManualOriginal struct {
	ID          int    `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Data        []byte `json:"data"`
}

// NewManualClient reuses the Paperless connector's existing configuration and
// authenticated HTTP client.
func NewManualClient(ds *types.DataSourceConfig) (*ManualClient, error) {
	cfg, err := parseConfig(ds)
	if err != nil {
		return nil, err
	}
	return &ManualClient{client: newClient(cfg.BaseURL, cfg.Token)}, nil
}

// Facets returns tags, document types, and correspondents, following all
// same-origin Paperless pagination links.
func (m *ManualClient) Facets(ctx context.Context) (*ManualFacets, error) {
	tags, err := m.client.listTags(ctx)
	if err != nil {
		return nil, err
	}
	documentTypes, err := m.client.listDocumentTypes(ctx)
	if err != nil {
		return nil, err
	}
	correspondents, err := m.client.listCorrespondents(ctx)
	if err != nil {
		return nil, err
	}
	return &ManualFacets{
		Tags:           facetOptionsFromTags(tags),
		DocumentTypes:  facetOptionsFromDocumentTypes(documentTypes),
		Correspondents: facetOptionsFromCorrespondents(correspondents),
	}, nil
}

// Documents returns exactly one page, clamping page size to the connector's
// bounded maximum. Paperless reports the complete match count separately.
func (m *ManualClient) Documents(ctx context.Context, filter ManualDocumentFilter) (*ManualDocumentsPage, error) {
	if filter.Page < 0 || filter.DocumentTypeID < 0 || filter.CorrespondentID < 0 {
		return nil, fmt.Errorf("paperless: page and facet IDs cannot be negative")
	}
	for _, id := range filter.Tags {
		if id <= 0 {
			return nil, fmt.Errorf("paperless: tag IDs must be positive")
		}
	}
	page := filter.Page
	if page == 0 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = manualDefaultPageSize
	}
	if pageSize > manualMaxPageSize {
		pageSize = manualMaxPageSize
	}
	values := url.Values{
		"page":      {strconv.Itoa(page)},
		"page_size": {strconv.Itoa(pageSize)},
	}
	if query := strings.TrimSpace(filter.Query); query != "" {
		values.Set("query", query)
	}
	if len(filter.Tags) > 0 {
		ids := make([]string, 0, len(filter.Tags))
		for _, id := range filter.Tags {
			ids = append(ids, strconv.Itoa(id))
		}
		values.Set("tags__id__all", strings.Join(ids, ","))
	}
	if filter.DocumentTypeID > 0 {
		values.Set("document_type__id", strconv.Itoa(filter.DocumentTypeID))
	}
	if filter.CorrespondentID > 0 {
		values.Set("correspondent__id", strconv.Itoa(filter.CorrespondentID))
	}
	var response listResponse[document]
	next, err := m.client.get(ctx, "/api/documents/", values, &response)
	if err != nil {
		return nil, err
	}
	results := make([]ManualDocument, 0, len(response.Results))
	for _, doc := range response.Results {
		results = append(results, ManualDocument{
			ID: doc.ID, Title: doc.Title, OriginalFileName: doc.OriginalFileName,
			Tags: append([]int(nil), doc.Tags...), DocumentTypeID: doc.DocumentType,
			CorrespondentID: doc.Correspondent,
		})
	}
	return &ManualDocumentsPage{
		Count: response.Count, Results: results, Page: page, PageSize: pageSize,
		HasNext: strings.TrimSpace(next) != "",
	}, nil
}

// Original returns the Paperless original document, rejecting the response if
// it exceeds maxBytes. The server metadata supplies the original filename.
func (m *ManualClient) Original(ctx context.Context, id int, maxBytes int64) (*ManualOriginal, error) {
	if id <= 0 {
		return nil, fmt.Errorf("paperless: document ID must be positive")
	}
	if maxBytes <= 0 || maxBytes == int64(^uint64(0)>>1) {
		return nil, fmt.Errorf("paperless: maximum original size must be positive and bounded")
	}
	doc, err := m.client.document(ctx, id)
	if err != nil {
		return nil, err
	}
	data, contentType, err := m.client.downloadOriginal(ctx, id, maxBytes)
	if err != nil {
		return nil, err
	}
	filename := strings.TrimSpace(doc.OriginalFileName)
	if filename == "" {
		title := strings.TrimSpace(doc.Title)
		if title == "" {
			title = fmt.Sprintf("Paperless document %d", id)
		}
		filename = title + ".pdf"
	}
	return &ManualOriginal{
		ID: id, Filename: datasource.SanitizeFileName(filename), ContentType: contentType, Data: data,
	}, nil
}

func facetOptionsFromTags(values []tag) []ManualFacetOption {
	out := make([]ManualFacetOption, 0, len(values))
	for _, value := range values {
		out = append(out, ManualFacetOption{ID: value.ID, Name: value.Name})
	}
	return out
}

func facetOptionsFromDocumentTypes(values []documentType) []ManualFacetOption {
	out := make([]ManualFacetOption, 0, len(values))
	for _, value := range values {
		out = append(out, ManualFacetOption{ID: value.ID, Name: value.Name})
	}
	return out
}

func facetOptionsFromCorrespondents(values []correspondent) []ManualFacetOption {
	out := make([]ManualFacetOption, 0, len(values))
	for _, value := range values {
		out = append(out, ManualFacetOption{ID: value.ID, Name: value.Name})
	}
	return out
}
