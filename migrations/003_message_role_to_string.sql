-- ============================================================
-- Migration: 003_message_role_to_string
-- Description: Change ai_chat_session_history.role from tinyint to
--              varchar storing langchaingo ChatMessageType values
--              (system|human|ai|generic|function|tool). The former
--              assistant_tool_call carrier (4) maps to 'function'.
-- ============================================================

-- UP
-- Widen the column first; existing tinyint values become their numeric strings.
ALTER TABLE ai_chat_session_history
    MODIFY COLUMN role VARCHAR(20) NOT NULL DEFAULT 'human' COMMENT 'Message role: system|human|ai|generic|function|tool';

-- Convert legacy numeric role codes to canonical role names.
UPDATE ai_chat_session_history SET role = CASE role
    WHEN '1' THEN 'human'
    WHEN '2' THEN 'ai'
    WHEN '3' THEN 'tool'
    WHEN '4' THEN 'function'
    ELSE 'human' END;

-- DOWN (rollback)
-- UPDATE ai_chat_session_history SET role = CASE role
--     WHEN 'human' THEN '1'
--     WHEN 'ai' THEN '2'
--     WHEN 'tool' THEN '3'
--     WHEN 'function' THEN '4'
--     ELSE '1' END;
-- ALTER TABLE ai_chat_session_history
--     MODIFY COLUMN role TINYINT NOT NULL DEFAULT 0 COMMENT 'Message role';
