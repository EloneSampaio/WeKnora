package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/datasource/connector/paperless"
	"github.com/gin-gonic/gin"
)

type PaperlessManualHandler struct {
	service *service.PaperlessManualService
}

func NewPaperlessManualHandler(s *service.PaperlessManualService) *PaperlessManualHandler {
	return &PaperlessManualHandler{service: s}
}

func paperlessError(c *gin.Context, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, access.ErrUnauthorized):
		status = http.StatusUnauthorized
	case errors.Is(err, access.ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, access.ErrNotFound):
		status = http.StatusNotFound
	}
	c.JSON(status, gin.H{"error": err.Error()})
}

func (h *PaperlessManualHandler) Facets(c *gin.Context) {
	facets, err := h.service.Facets(c.Request.Context(), c.Param("id"))
	if err != nil {
		paperlessError(c, err)
		return
	}
	c.JSON(http.StatusOK, facets)
}

func (h *PaperlessManualHandler) Documents(c *gin.Context) {
	filter := paperless.ManualDocumentFilter{Query: c.Query("query")}
	for key, target := range map[string]*int{"page": &filter.Page, "page_size": &filter.PageSize, "document_type": &filter.DocumentTypeID, "correspondent": &filter.CorrespondentID} {
		if raw := c.Query(key); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value <= 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid " + key})
				return
			}
			*target = value
		}
	}
	if raw := c.Query("tags"); raw != "" {
		if len(raw) > 2048 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "too many tags"})
			return
		}
		for _, rawID := range strings.Split(raw, ",") {
			id, err := strconv.Atoi(rawID)
			if err != nil || id <= 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tags"})
				return
			}
			filter.Tags = append(filter.Tags, id)
		}
	}
	page, err := h.service.Documents(c.Request.Context(), c.Param("id"), filter)
	if err != nil {
		paperlessError(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

func (h *PaperlessManualHandler) Import(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	var req service.PaperlessImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid import request"})
		return
	}
	result, err := h.service.Import(c.Request.Context(), c.Param("id"), req)
	if err != nil {
		paperlessError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
