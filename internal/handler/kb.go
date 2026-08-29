package handler

import (
	"errors"
	"net/http"
	"strconv"

	"ansmeee-ai-agent/internal/agent"
	"ansmeee-ai-agent/internal/kb"
	"ansmeee-ai-agent/internal/middleware"
	"ansmeee-ai-agent/internal/models"
	"ansmeee-ai-agent/internal/tracing"
	"ansmeee-ai-agent/pkg/logger"
	"ansmeee-ai-agent/pkg/response"

	"github.com/gin-gonic/gin"
)

// KBHandler handles agent knowledge-base requests.
type KBHandler struct {
	mgr        *kb.KBManager
	agentStore *agent.AgentStore
}

// NewKBHandler creates a new KB handler.
func NewKBHandler(mgr *kb.KBManager, agentStore *agent.AgentStore) *KBHandler {
	return &KBHandler{mgr: mgr, agentStore: agentStore}
}

func (h *KBHandler) userID(c *gin.Context) int64 {
	return c.GetInt64(middleware.CtxUserID)
}

// verifyAgent 确认 agent 属于当前用户，返回 agent UUID。
func (h *KBHandler) verifyAgent(c *gin.Context) (string, bool) {
	ctx := c.Request.Context()
	agentID := c.Param("id")
	if _, err := h.agentStore.Get(ctx, agentID, h.userID(c)); err != nil {
		if errors.Is(err, agent.ErrAgentNotFound) {
			response.Fail(c, http.StatusNotFound, response.CodeNotFound, "agent not found")
		} else {
			logger.L().Error("verify agent failed", tracing.ErrFields(ctx, err)...)
			response.InternalError(c, err.Error())
		}
		return "", false
	}
	return agentID, true
}

// ---------- KB 元数据 ----------

// GetKB 返回 agent 的知识库元数据。
func (h *KBHandler) GetKB(c *gin.Context) {
	ctx := c.Request.Context()
	agentID, ok := h.verifyAgent(c)
	if !ok {
		return
	}
	k, err := h.mgr.GetKB(ctx, agentID)
	if err != nil {
		if errors.Is(err, kb.ErrKBNotFound) {
			response.Fail(c, http.StatusNotFound, response.CodeNotFound, "knowledge base not found")
			return
		}
		logger.L().Error("get kb failed", tracing.ErrFields(ctx, err)...)
		response.InternalError(c, err.Error())
		return
	}
	response.OK(c, k)
}

type kbUpsertRequest struct {
	Title           string  `json:"title"`
	Description     string  `json:"description"`
	Enabled         *bool   `json:"enabled"`
	AlwaysInject    *bool   `json:"always_inject"`
	ShowCitations   *bool   `json:"show_citations"`
	TopK            int     `json:"top_k"`
	MinSimilarity   float64 `json:"min_similarity"`
	BudgetRatio     float64 `json:"budget_ratio"`
	MaxCharsPerTurn int     `json:"max_chars_per_turn"`
}

// UpsertKB 创建或更新 agent 知识库配置。
func (h *KBHandler) UpsertKB(c *gin.Context) {
	ctx := c.Request.Context()
	agentID, ok := h.verifyAgent(c)
	if !ok {
		return
	}
	var req kbUpsertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}
	patch := &models.KnowledgeBase{
		Title:           req.Title,
		Description:     req.Description,
		TopK:            req.TopK,
		MinSimilarity:   req.MinSimilarity,
		BudgetRatio:     req.BudgetRatio,
		MaxCharsPerTurn: req.MaxCharsPerTurn,
	}
	if req.Enabled != nil {
		patch.Enabled = *req.Enabled
	} else {
		patch.Enabled = true
	}
	if req.AlwaysInject != nil {
		patch.AlwaysInject = *req.AlwaysInject
	} else {
		patch.AlwaysInject = true
	}
	if req.ShowCitations != nil {
		patch.ShowCitations = *req.ShowCitations
	} else {
		patch.ShowCitations = true
	}
	k, err := h.mgr.UpsertKB(ctx, agentID, patch)
	if err != nil {
		logger.L().Error("upsert kb failed", tracing.ErrFields(ctx, err)...)
		response.InternalError(c, err.Error())
		return
	}
	response.OK(c, k)
}

// ---------- 文档管理 ----------

// ListDocs 分页列出知识库文档。
func (h *KBHandler) ListDocs(c *gin.Context) {
	ctx := c.Request.Context()
	agentID, ok := h.verifyAgent(c)
	if !ok {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	docs, total, err := h.mgr.ListDocs(ctx, agentID, page, pageSize)
	if err != nil {
		if errors.Is(err, kb.ErrKBNotFound) {
			response.OK(c, gin.H{"docs": []*models.KBDoc{}, "total": 0})
			return
		}
		logger.L().Error("list kb docs failed", tracing.ErrFields(ctx, err)...)
		response.InternalError(c, err.Error())
		return
	}
	response.OK(c, gin.H{"docs": docs, "total": total})
}

// AddDoc 创建文档并同步索引。
func (h *KBHandler) AddDoc(c *gin.Context) {
	ctx := c.Request.Context()
	agentID, ok := h.verifyAgent(c)
	if !ok {
		return
	}
	var req kb.AddDocRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}
	doc, err := h.mgr.AddDoc(ctx, agentID, &req)
	if err != nil {
		logger.L().Error("add kb doc failed", tracing.ErrFields(ctx, err)...)
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, doc)
}

// GetDoc 返回单个文档。
func (h *KBHandler) GetDoc(c *gin.Context) {
	ctx := c.Request.Context()
	if _, ok := h.verifyAgent(c); !ok {
		return
	}
	docID, err := strconv.ParseInt(c.Param("docId"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid doc id")
		return
	}
	doc, err := h.mgr.GetDoc(ctx, docID)
	if err != nil {
		if errors.Is(err, kb.ErrDocNotFound) {
			response.Fail(c, http.StatusNotFound, response.CodeNotFound, "document not found")
			return
		}
		logger.L().Error("get kb doc failed", tracing.ErrFields(ctx, err)...)
		response.InternalError(c, err.Error())
		return
	}
	response.OK(c, doc)
}

// DeleteDoc 软删除文档。
func (h *KBHandler) DeleteDoc(c *gin.Context) {
	ctx := c.Request.Context()
	agentID, ok := h.verifyAgent(c)
	if !ok {
		return
	}
	docID, err := strconv.ParseInt(c.Param("docId"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid doc id")
		return
	}
	if err := h.mgr.DeleteDoc(ctx, agentID, docID); err != nil {
		if errors.Is(err, kb.ErrDocNotFound) {
			response.Fail(c, http.StatusNotFound, response.CodeNotFound, "document not found")
			return
		}
		logger.L().Error("delete kb doc failed", tracing.ErrFields(ctx, err)...)
		response.InternalError(c, err.Error())
		return
	}
	response.OK(c, gin.H{"deleted": true})
}

type reindexRequest struct {
	Content string `json:"content"`
}

// ReindexDoc 重新索引文档。
func (h *KBHandler) ReindexDoc(c *gin.Context) {
	ctx := c.Request.Context()
	if _, ok := h.verifyAgent(c); !ok {
		return
	}
	docID, err := strconv.ParseInt(c.Param("docId"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid doc id")
		return
	}
	var req reindexRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}
	if req.Content == "" {
		response.BadRequest(c, "content is required")
		return
	}
	if err := h.mgr.ReindexDoc(ctx, docID, req.Content); err != nil {
		logger.L().Error("reindex kb doc failed", tracing.ErrFields(ctx, err)...)
		response.InternalError(c, err.Error())
		return
	}
	response.OK(c, gin.H{"reindexed": true})
}

// ---------- 链路 B：显式检索 ----------

type kbQueryRequest struct {
	Query string `json:"query" binding:"required"`
}

// Query 执行显式知识库检索（调试 / 链路 B 验证）。
func (h *KBHandler) Query(c *gin.Context) {
	ctx := c.Request.Context()
	agentID, ok := h.verifyAgent(c)
	if !ok {
		return
	}
	var req kbQueryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "query is required")
		return
	}
	result, err := h.mgr.Query(ctx, agentID, req.Query)
	if err != nil {
		if errors.Is(err, kb.ErrKBNotFound) {
			response.Fail(c, http.StatusNotFound, response.CodeNotFound, "knowledge base not found")
			return
		}
		if errors.Is(err, kb.ErrKBDisabled) {
			response.BadRequest(c, "knowledge base disabled")
			return
		}
		logger.L().Error("kb query failed", tracing.ErrFields(ctx, err)...)
		response.InternalError(c, err.Error())
		return
	}
	response.OK(c, result)
}
