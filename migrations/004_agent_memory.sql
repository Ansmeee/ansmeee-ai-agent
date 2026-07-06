-- ============================================================
-- Migration: 004_agent_memory
-- Description: Phase 1 Agent memory mechanism schema.
--   1. ai_chat_session: add idle-scan activity columns
--      (last_active_at, summarized) driving the L2 summary trigger.
--   2. memory_entries: new L2 long-term memory table (fact / policy
--      channels). uk_uakv leads with user_id so per-user memory is
--      isolated and matches the Admit OnConflict target.
--   3. session_summaries: new per-session LLM summary table (Phase 2).
--
-- NOTE: memory_entries / session_summaries are also created by GORM
-- AutoMigrate in NewFactStore. These statements are the authoritative
-- schema for SQL-managed deployments and are idempotent (IF NOT EXISTS).
-- The uk_uakv rebuild below corrects any DB where an earlier AutoMigrate
-- already built the index without user_id.
-- ============================================================

-- UP

-- 1. ai_chat_session idle-scan columns (no other migration path exists).
ALTER TABLE ai_chat_session
    ADD COLUMN last_active_at DATETIME NULL COMMENT 'Last message time; drives idle-scan',
    ADD COLUMN summarized TINYINT(1) NOT NULL DEFAULT 0 COMMENT 'Whether the session was summarized into L2';

-- 2. L2 long-term memory table.
CREATE TABLE IF NOT EXISTS memory_entries (
    id          BIGINT       NOT NULL AUTO_INCREMENT,
    user_id     BIGINT       NOT NULL,
    agent_id    VARCHAR(36)  NOT NULL DEFAULT '' COMMENT "'' = user-level memory (not NULL)",
    channel     VARCHAR(16)  NOT NULL COMMENT 'fact | policy',
    kind        VARCHAR(16)  NOT NULL COMMENT 'fact | preference | policy | summary',
    key_name    VARCHAR(128) NOT NULL,
    value       TEXT         NOT NULL,
    value_hash  CHAR(32)     NOT NULL COMMENT 'md5(value) for multi-value dedup',
    cardinality VARCHAR(8)   NOT NULL DEFAULT 'multi' COMMENT 'single | multi',
    confidence  DOUBLE       NOT NULL DEFAULT 1.0,
    evidence    JSON         NULL COMMENT '[{session_id, message_id}]',
    status      VARCHAR(8)   NOT NULL DEFAULT 'active' COMMENT 'active | archived',
    source      VARCHAR(16)  NOT NULL DEFAULT 'rule' COMMENT 'rule | user_stated | llm_extracted',
    hit_count   INT          NOT NULL DEFAULT 0,
    last_used_at DATETIME    NULL,
    created_at  DATETIME     NULL,
    updated_at  DATETIME     NULL,
    expires_at  DATETIME     NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_uakv (user_id, agent_id, key_name, value_hash),
    KEY idx_user_channel (user_id, channel),
    KEY idx_user_key (user_id, key_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='L2 long-term memory (fact / policy)';

-- 3. Per-session LLM summary table (Phase 2 path).
CREATE TABLE IF NOT EXISTS session_summaries (
    id         BIGINT      NOT NULL AUTO_INCREMENT,
    session_id VARCHAR(36) NULL,
    user_id    BIGINT      NULL,
    summary    TEXT        NULL,
    topics     JSON        NULL,
    created_at DATETIME    NULL,
    PRIMARY KEY (id),
    KEY idx_session_summaries_session (session_id),
    KEY idx_session_summaries_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Per-session LLM summary (L2 secondary path)';

-- Corrective: if a prior AutoMigrate built uk_uakv WITHOUT user_id, rebuild it.
-- Safe to run once on such a DB; skip (or ignore the error) where already correct.
-- ALTER TABLE memory_entries
--     DROP INDEX uk_uakv,
--     ADD UNIQUE INDEX uk_uakv (user_id, agent_id, key_name, value_hash);

-- DOWN (rollback)
-- DROP TABLE IF EXISTS session_summaries;
-- DROP TABLE IF EXISTS memory_entries;
-- ALTER TABLE ai_chat_session
--     DROP COLUMN summarized,
--     DROP COLUMN last_active_at;
