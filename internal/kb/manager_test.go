package kb

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"ansmeee-ai-agent/internal/models"
)

// ---------- fake stores ----------

type fakeKBStore struct {
	mu       sync.Mutex
	kbs      map[string]*models.KnowledgeBase
	docs     int
	chunks   int
}

func newFakeKBStore() *fakeKBStore {
	return &fakeKBStore{kbs: make(map[string]*models.KnowledgeBase)}
}

func (s *fakeKBStore) GetByAgent(_ context.Context, agentID string) (*models.KnowledgeBase, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kb, ok := s.kbs[agentID]
	if !ok {
		return nil, ErrKBNotFound
	}
	cp := *kb
	return &cp, nil
}

func (s *fakeKBStore) UpsertByAgent(_ context.Context, agentID string, patch *models.KnowledgeBase) (*models.KnowledgeBase, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.kbs[agentID]; ok {
		if patch.Title != "" {
			existing.Title = patch.Title
		}
		existing.Enabled = patch.Enabled
		existing.AlwaysInject = patch.AlwaysInject
		existing.ShowCitations = patch.ShowCitations
		if patch.TopK > 0 {
			existing.TopK = patch.TopK
		}
		if patch.MinSimilarity > 0 {
			existing.MinSimilarity = patch.MinSimilarity
		}
		if patch.MaxCharsPerTurn > 0 {
			existing.MaxCharsPerTurn = patch.MaxCharsPerTurn
		}
		cp := *existing
		return &cp, nil
	}
	kb := &models.KnowledgeBase{
		AgentID:         agentID,
		Title:           patch.Title,
		Enabled:         patch.Enabled,
		AlwaysInject:    patch.AlwaysInject,
		ShowCitations:   patch.ShowCitations,
		TopK:            patch.TopK,
		MinSimilarity:   patch.MinSimilarity,
		MaxCharsPerTurn: patch.MaxCharsPerTurn,
		Status:          models.KBStatusActive,
	}
	applyKBDefaults(kb)
	if kb.Title == "" {
		kb.Title = "默认知识库"
	}
	s.kbs[agentID] = kb
	cp := *kb
	return &cp, nil
}

func (s *fakeKBStore) UpdateCounters(_ context.Context, kbID int64, deltaDocs, deltaChunks int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs += deltaDocs
	s.chunks += deltaChunks
	return nil
}

type fakeDocStore struct {
	mu    sync.Mutex
	docs  map[int64]*models.KBDoc
	next  int64
}

func newFakeDocStore() *fakeDocStore {
	return &fakeDocStore{docs: make(map[int64]*models.KBDoc)}
}

func (s *fakeDocStore) Create(_ context.Context, doc *models.KBDoc) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	doc.ID = s.next
	cp := *doc
	s.docs[doc.ID] = &cp
	return doc.ID, nil
}

func (s *fakeDocStore) Get(_ context.Context, docID int64) (*models.KBDoc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.docs[docID]
	if !ok {
		return nil, ErrDocNotFound
	}
	cp := *d
	return &cp, nil
}

func (s *fakeDocStore) ListByKB(_ context.Context, kbID int64, page, pageSize int) ([]*models.KBDoc, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*models.KBDoc
	for _, d := range s.docs {
		if d.KBID == kbID && d.Status != models.DocStatusArchived {
			cp := *d
			out = append(out, &cp)
		}
	}
	return out, int64(len(out)), nil
}

func (s *fakeDocStore) UpdateStatus(_ context.Context, docID int64, status int8, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.docs[docID]; ok {
		d.Status = status
		d.ErrorMsg = msg
	}
	return nil
}

func (s *fakeDocStore) UpdateMeta(_ context.Context, docID int64, charCount, chunkCount int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.docs[docID]; ok {
		d.CharCount = charCount
		d.ChunkCount = chunkCount
		d.Status = models.DocStatusReady
		d.ErrorMsg = ""
	}
	return nil
}

func (s *fakeDocStore) Delete(_ context.Context, docID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.docs[docID]; ok {
		d.Status = models.DocStatusArchived
	}
	return nil
}

type fakeChunkStore struct {
	mu     sync.Mutex
	chunks []*models.KBChunk
	nextID int64
}

func newFakeChunkStore() *fakeChunkStore { return &fakeChunkStore{} }

func (s *fakeChunkStore) BatchUpsert(_ context.Context, chunks []*models.KBChunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range chunks {
		s.nextID++
		c.ID = s.nextID
		c.VectorID = fmt.Sprintf("%d", c.ID)
		cp := *c
		s.chunks = append(s.chunks, &cp)
	}
	return nil
}

func (s *fakeChunkStore) KeywordSearch(_ context.Context, agentID, query string, topK int) ([]RetrievedChunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if topK <= 0 {
		topK = 5
	}
	var out []RetrievedChunk
	for _, c := range s.chunks {
		if c.AgentID != agentID {
			continue
		}
		if strings.Contains(c.Text, query) {
			out = append(out, RetrievedChunk{
				ChunkID:  c.ID,
				DocID:    c.DocID,
				DocTitle: c.DocTitle,
				ChunkIdx: c.ChunkIndex,
				Text:     c.Text,
				Score:    0.8,
				Channel:  "keyword",
			})
		}
	}
	if len(out) > topK {
		out = out[:topK]
	}
	return out, nil
}

func (s *fakeChunkStore) DeleteByDoc(_ context.Context, docID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := s.chunks[:0]
	for _, c := range s.chunks {
		if c.DocID != docID {
			filtered = append(filtered, c)
		}
	}
	s.chunks = filtered
	return nil
}

func (s *fakeChunkStore) CountByDoc(_ context.Context, docID int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for _, c := range s.chunks {
		if c.DocID == docID {
			n++
		}
	}
	return n, nil
}

func (s *fakeChunkStore) ListByDoc(_ context.Context, docID int64) ([]*models.KBChunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*models.KBChunk, 0)
	for _, c := range s.chunks {
		if c.DocID == docID {
			cp := *c
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (s *fakeChunkStore) GetByIDs(_ context.Context, ids []int64) (map[int64]*models.KBChunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[int64]*models.KBChunk, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	set := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	for _, c := range s.chunks {
		if _, ok := set[c.ID]; ok {
			cp := *c
			out[c.ID] = &cp
		}
	}
	return out, nil
}

// fakeEmbedder 用文本长度构造确定性向量，让相同/相近文本余弦相似度高。
type fakeEmbedder struct{}

func (fakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	// 简单确定性嵌入：按字符 bigram hash 到 16 维，归一化
	vec := make([]float32, 16)
	runes := []rune(text)
	for i := 0; i+1 < len(runes); i++ {
		h := uint32(2166136261)
		for _, b := range []byte(string(runes[i : i+2])) {
			h ^= uint32(b)
			h *= 16777619
		}
		vec[h%16] += 1
	}
	if len(runes) == 0 {
		return vec, nil
	}
	// 归一化
	var sum float64
	for _, v := range vec {
		sum += float64(v) * float64(v)
	}
	if sum > 0 {
		inv := float32(1.0 / sqrt64(sum))
		for i := range vec {
			vec[i] *= inv
		}
	}
	return vec, nil
}

func sqrt64(x float64) float64 {
	// 牛顿法，避免 import math
	if x == 0 {
		return 0
	}
	z := x
	for i := 0; i < 20; i++ {
		z = (z + x/z) / 2
	}
	return z
}

// ---------- 测试装配 ----------

func setupTestManager(t *testing.T) (*KBManager, *fakeKBStore, *fakeDocStore, *fakeChunkStore) {
	t.Helper()
	kbStore := newFakeKBStore()
	docStore := newFakeDocStore()
	chunkStore := newFakeChunkStore()
	vecStore := NewInMemoryVectorStore()
	retriever := NewHybridRetriever(vecStore, chunkStore, 0.5, 0.5)
	indexer := NewIndexer(kbStore, docStore, chunkStore, vecStore, NewDocParser(), NewRecursiveChunker(), fakeEmbedder{})
	mgr := NewKBManager(kbStore, docStore, chunkStore, retriever, indexer, fakeEmbedder{}, WithVectorStore(vecStore))
	return mgr, kbStore, docStore, chunkStore
}

// ---------- 端到端链路测试 ----------

func TestKBManager_AddDocAndQuery(t *testing.T) {
	mgr, kbStore, _, chunkStore := setupTestManager(t)
	ctx := context.Background()
	agentID := "agent-uuid-001"

	// 准备一份测试文档：包含明确的召回线索
	content := `# 产品手册

TRAE 是一款 AI 原生编程协作工具，支持智能代码补全与多轮对话。

退款政策：购买后 7 天内可申请全额退款，需提供订单号。

联系方式：support@trae.com，工作日 9:00-18:00 响应。`

	// 1. 添加文档（触发同步索引流水线）
	doc, err := mgr.AddDoc(ctx, agentID, &AddDocRequest{
		Title:      "TRAE 产品手册",
		SourceType: models.SourceTypeMarkdown,
		Content:    content,
		ParseConfig: models.KBParseConfig{
			ChunkSize: 100, ChunkOverlap: 20,
		},
	})
	if err != nil {
		t.Fatalf("AddDoc failed: %v", err)
	}
	if doc.Status != models.DocStatusReady {
		t.Fatalf("expected doc status ready, got %d (msg=%s)", doc.Status, doc.ErrorMsg)
	}
	if doc.ChunkCount == 0 {
		t.Fatal("expected chunk_count > 0")
	}
	t.Logf("indexed doc: id=%d chunks=%d chars=%d", doc.ID, doc.ChunkCount, doc.CharCount)

	// 验证 chunk 落库 + 向量落库
	if len(chunkStore.chunks) != doc.ChunkCount {
		t.Fatalf("chunk store has %d chunks, doc says %d", len(chunkStore.chunks), doc.ChunkCount)
	}

	// 验证 KB 元数据自动创建 + 计数累加
	kb, err := kbStore.GetByAgent(ctx, agentID)
	if err != nil {
		t.Fatalf("kb not auto-created: %v", err)
	}
	if kbStore.docs != 1 || kbStore.chunks != doc.ChunkCount {
		t.Fatalf("kb counters wrong: docs=%d chunks=%d (want %d)", kbStore.docs, kbStore.chunks, doc.ChunkCount)
	}
	_ = kb

	// 2. 链路 B：显式检索（关键词命中"退款"）
	result, err := mgr.Query(ctx, agentID, "退款政策")
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(result.Chunks) == 0 {
		t.Fatal("expected non-empty query result")
	}
	t.Logf("query returned %d chunks, top score=%.4f channel=%s", len(result.Chunks), result.Chunks[0].Score, result.Chunks[0].Channel)
	// 验证命中退款相关内容
	combined := strings.Join(chunkTexts(result.Chunks), " ")
	if !strings.Contains(combined, "退款") {
		t.Fatalf("retrieved chunks do not mention 退款: %s", combined)
	}
}

func TestKBManager_Inject_LinkA(t *testing.T) {
	mgr, _, _, _ := setupTestManager(t)
	ctx := context.Background()
	agentID := "agent-uuid-002"

	// 索引文档
	content := "服务器告警阈值：CPU 持续 5 分钟超过 90% 触发 P1 告警，内存超过 95% 触发 P2 告警。"
	if _, err := mgr.AddDoc(ctx, agentID, &AddDocRequest{
		Title: "运维手册", SourceType: models.SourceTypeText, Content: content,
	}); err != nil {
		t.Fatalf("AddDoc failed: %v", err)
	}

	// 链路 A：注入到 system prompt
	injected := mgr.Inject(ctx, agentID, "CPU 告警阈值是多少")
	if injected == "" {
		t.Fatal("expected non-empty injection")
	}
	if !strings.Contains(injected, "【知识库参考】") {
		t.Fatalf("injection missing header: %q", injected)
	}
	if !strings.Contains(injected, "90%") {
		t.Fatalf("injection missing key fact 90%%: %q", injected)
	}
	t.Logf("injected segment:\n%s", injected)
}

func TestKBManager_DisabledKB_NoInjection(t *testing.T) {
	mgr, kbStore, _, _ := setupTestManager(t)
	ctx := context.Background()
	agentID := "agent-uuid-003"

	// 先索引一个文档（自动建 KB）
	if _, err := mgr.AddDoc(ctx, agentID, &AddDocRequest{
		Title: "文档", SourceType: models.SourceTypeText, Content: "测试内容",
	}); err != nil {
		t.Fatalf("AddDoc failed: %v", err)
	}
	// 关闭注入
	if _, err := kbStore.UpsertByAgent(ctx, agentID, &models.KnowledgeBase{
		Enabled: false, AlwaysInject: false,
	}); err != nil {
		t.Fatalf("disable kb failed: %v", err)
	}
	// 注入应为空
	if got := mgr.Inject(ctx, agentID, "测试内容"); got != "" {
		t.Fatalf("expected empty injection when disabled, got: %q", got)
	}
	// Query 也应被 KB disabled 拦截
	if _, err := mgr.Query(ctx, agentID, "测试内容"); err != ErrKBDisabled {
		t.Fatalf("expected ErrKBDisabled, got %v", err)
	}
}

func TestKBManager_DeleteDoc_CountersRollback(t *testing.T) {
	mgr, kbStore, _, _ := setupTestManager(t)
	ctx := context.Background()
	agentID := "agent-uuid-004"

	doc, err := mgr.AddDoc(ctx, agentID, &AddDocRequest{
		Title: "待删除文档", SourceType: models.SourceTypeText,
		Content: strings.Repeat("这是一段测试文本。", 20),
	})
	if err != nil {
		t.Fatalf("AddDoc failed: %v", err)
	}
	chunksBefore := kbStore.chunks
	t.Logf("before delete: docs=%d chunks=%d", kbStore.docs, kbStore.chunks)

	// 删除文档
	if err := mgr.DeleteDoc(ctx, agentID, doc.ID); err != nil {
		t.Fatalf("DeleteDoc failed: %v", err)
	}
	if kbStore.docs != 0 || kbStore.chunks != 0 {
		t.Fatalf("counters not rolled back: docs=%d chunks=%d (want 0/0)", kbStore.docs, kbStore.chunks)
	}
	if chunksBefore == 0 {
		t.Fatal("sanity: chunksBefore was 0")
	}
	// 验证删除后检索不到
	res, err := mgr.Query(ctx, agentID, "测试文本")
	if err != nil {
		t.Fatalf("Query after delete failed: %v", err)
	}
	if len(res.Chunks) != 0 {
		t.Fatalf("expected 0 chunks after delete, got %d", len(res.Chunks))
	}
}

func TestKBManager_Reindex(t *testing.T) {
	mgr, _, docStore, _ := setupTestManager(t)
	ctx := context.Background()
	agentID := "agent-uuid-005"

	// 初始文档：短内容，1 个 chunk
	doc, err := mgr.AddDoc(ctx, agentID, &AddDocRequest{
		Title: "v1", SourceType: models.SourceTypeText, Content: "旧内容旧内容旧内容",
	})
	if err != nil {
		t.Fatalf("AddDoc failed: %v", err)
	}
	if doc.ChunkCount != 1 {
		t.Fatalf("expected 1 chunk initially, got %d", doc.ChunkCount)
	}

	// 重新索引为更长内容（>512 rune，应切出多个 chunk）
	newContent := strings.Repeat("全新的知识库内容片段。", 100) // ~1200 rune
	if err := mgr.ReindexDoc(ctx, doc.ID, newContent); err != nil {
		t.Fatalf("ReindexDoc failed: %v", err)
	}
	updated, _ := docStore.Get(ctx, doc.ID)
	if updated.ChunkCount <= 1 {
		t.Fatalf("reindex did not produce more chunks: old=1 new=%d", updated.ChunkCount)
	}
	t.Logf("reindex: old chunks=1, new chunks=%d", updated.ChunkCount)

	// 验证旧内容已被替换（检索"旧内容"应无命中）
	res, _ := mgr.Query(ctx, agentID, "旧内容")
	if len(res.Chunks) != 0 {
		t.Fatalf("expected old content purged, got %d hits", len(res.Chunks))
	}
	// 新内容可检索
	res2, _ := mgr.Query(ctx, agentID, "知识库内容")
	if len(res2.Chunks) == 0 {
		t.Fatal("expected new content retrievable")
	}
}

func TestKBManager_InjectBudgetTrim(t *testing.T) {
	mgr, kbStore, _, _ := setupTestManager(t)
	ctx := context.Background()
	agentID := "agent-uuid-006"

	// 索引一个大文档
	content := strings.Repeat("预算裁剪测试内容段落。", 100)
	if _, err := mgr.AddDoc(ctx, agentID, &AddDocRequest{
		Title: "大文档", SourceType: models.SourceTypeText, Content: content,
	}); err != nil {
		t.Fatalf("AddDoc failed: %v", err)
	}
	// 设置很小的预算
	if _, err := kbStore.UpsertByAgent(ctx, agentID, &models.KnowledgeBase{
		Enabled: true, AlwaysInject: true, ShowCitations: true,
		MaxCharsPerTurn: 50,
	}); err != nil {
		t.Fatalf("set budget failed: %v", err)
	}
	injected := mgr.Inject(ctx, agentID, "预算")
	if injected == "" {
		t.Fatal("expected non-empty injection")
	}
	// 提取【知识库参考】之后的正文，验证不超过预算（含 header 容差）
	body := strings.TrimSpace(strings.SplitN(injected, "【知识库参考】", 2)[1])
	if len([]rune(body)) > 50+50 { // header + 编号容差
		t.Fatalf("injection body too long: %d runes", len([]rune(body)))
	}
	t.Logf("budget-trimmed injection (%d runes):\n%s", len([]rune(body)), injected)
}

func chunkTexts(cs []RetrievedChunk) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Text
	}
	return out
}
