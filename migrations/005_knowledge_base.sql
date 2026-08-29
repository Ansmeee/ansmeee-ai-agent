-- ============================================================
-- Migration 005: Agent 知识库体系（4 张表 + 复合索引）
-- 依赖：现有 ai_agent.uuid (char(36)) 做外键逻辑关联
-- ============================================================

CREATE TABLE IF NOT EXISTS kb (
  id                BIGINT AUTO_INCREMENT PRIMARY KEY,
  agent_id          CHAR(36)        NOT NULL COMMENT '外键 → ai_agent.uuid，1 agent=1 kb MVP',
  title             VARCHAR(255)    NOT NULL DEFAULT '',
  description       VARCHAR(1000)   NOT NULL DEFAULT '',
  enabled           TINYINT(1)      NOT NULL DEFAULT 1 COMMENT '总开关',
  always_inject     TINYINT(1)      NOT NULL DEFAULT 1 COMMENT '链路 A：隐式注入',
  show_citations    TINYINT(1)      NOT NULL DEFAULT 1,
  top_k             INT             NOT NULL DEFAULT 5,
  min_similarity    DOUBLE          NOT NULL DEFAULT 0.6,
  budget_ratio      DOUBLE          NOT NULL DEFAULT 0.3,
  max_chars_per_turn INT            NOT NULL DEFAULT 4000,
  doc_count         INT             NOT NULL DEFAULT 0,
  chunk_count       INT             NOT NULL DEFAULT 0,
  status            TINYINT         NOT NULL DEFAULT 1 COMMENT '1=active,2=archived',
  mtime             DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  ctime             DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uk_agent_id (agent_id),
  KEY idx_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Agent 知识库元数据';

CREATE TABLE IF NOT EXISTS kb_docs (
  id               BIGINT AUTO_INCREMENT PRIMARY KEY,
  kb_id            BIGINT          NOT NULL,
  agent_id         CHAR(36)        NOT NULL,
  title            VARCHAR(500)    NOT NULL DEFAULT '',
  source_type      VARCHAR(16)     NOT NULL DEFAULT 'upload' COMMENT 'upload/url/text/markdown',
  source_url       VARCHAR(1000)   NOT NULL DEFAULT '',
  file_name        VARCHAR(255)    NOT NULL DEFAULT '',
  content_path     VARCHAR(1000)   NOT NULL DEFAULT '' COMMENT '对象存储 / 本地路径',
  char_count       INT             NOT NULL DEFAULT 0,
  chunk_count      INT             NOT NULL DEFAULT 0,
  parse_config     JSON            DEFAULT NULL COMMENT 'KBParseConfig 快照',
  tags             JSON            DEFAULT NULL,
  status           TINYINT         NOT NULL DEFAULT 0 COMMENT '0=pending,1=indexing,2=ready,3=failed,4=archived',
  error_msg        VARCHAR(1000)   NOT NULL DEFAULT '',
  last_indexed_at  DATETIME        DEFAULT NULL,
  mtime            DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  ctime            DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_kb_agent (kb_id, agent_id),
  KEY idx_status (status),
  KEY idx_ctime (ctime)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='知识库文档';

CREATE TABLE IF NOT EXISTS kb_chunks (
  id           BIGINT AUTO_INCREMENT PRIMARY KEY,
  kb_id        BIGINT        NOT NULL,
  agent_id     CHAR(36)      NOT NULL,
  doc_id       BIGINT        NOT NULL,
  chunk_index  INT           NOT NULL DEFAULT 0 COMMENT '同一 doc 内顺序号',
  text         TEXT          NOT NULL COMMENT '分片纯文本（BM25 + 回显用）',
  char_count   INT           NOT NULL DEFAULT 0,
  doc_title    VARCHAR(500)  NOT NULL DEFAULT '' COMMENT '冗余，避免 JOIN',
  vector_id    VARCHAR(64)   NOT NULL DEFAULT '' COMMENT '向量库主键 = chunk_id str',
  mtime        DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  ctime        DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_chunk_kb (kb_id),
  KEY idx_chunk_agent (agent_id),
  KEY idx_chunk_doc (doc_id),
  KEY idx_vector_id (vector_id),
  FULLTEXT KEY ft_text (text) WITH PARSER ngram COMMENT '中文全文检索 BM25 关键词通道'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='文档分片 + 全文索引';

CREATE TABLE IF NOT EXISTS kb_index_jobs (
  id            BIGINT AUTO_INCREMENT PRIMARY KEY,
  doc_id        BIGINT          NOT NULL,
  agent_id      CHAR(36)        NOT NULL,
  step          VARCHAR(16)     NOT NULL DEFAULT 'pending',
  progress      INT             NOT NULL DEFAULT 0,
  progress_msg  VARCHAR(500)    NOT NULL DEFAULT '',
  stats         JSON            DEFAULT NULL,
  error_msg     VARCHAR(2000)   NOT NULL DEFAULT '',
  started_at    DATETIME        DEFAULT NULL,
  finished_at   DATETIME        DEFAULT NULL,
  ctime         DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_doc (doc_id),
  KEY idx_agent (agent_id),
  KEY idx_step (step),
  KEY idx_ctime (ctime)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='文档索引异步任务';
