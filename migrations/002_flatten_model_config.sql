-- ============================================================
-- Migration: 002_flatten_model_config
-- Description: Promote model_config JSON column into independent
--              columns (model/temperature/max_tokens/top_p) on ai_agent.
-- ============================================================

-- UP
ALTER TABLE ai_agent
    ADD COLUMN model VARCHAR(100) NOT NULL DEFAULT '' COMMENT 'Per-agent model name override' AFTER tools,
    ADD COLUMN temperature DOUBLE DEFAULT NULL COMMENT 'Sampling temperature; NULL means not set' AFTER model,
    ADD COLUMN max_tokens INT NOT NULL DEFAULT 0 COMMENT 'Max output tokens; 0 means not set' AFTER temperature,
    ADD COLUMN top_p DOUBLE DEFAULT NULL COMMENT 'Nucleus sampling top_p; NULL means not set' AFTER max_tokens;

-- Backfill from existing model_config JSON blob.
UPDATE ai_agent SET
    model = COALESCE(JSON_UNQUOTE(JSON_EXTRACT(model_config, '$.model')), ''),
    temperature = JSON_EXTRACT(model_config, '$.temperature'),
    max_tokens = COALESCE(JSON_EXTRACT(model_config, '$.max_tokens'), 0),
    top_p = JSON_EXTRACT(model_config, '$.top_p')
WHERE model_config IS NOT NULL;

ALTER TABLE ai_agent DROP COLUMN model_config;

-- DOWN (rollback)
-- ALTER TABLE ai_agent ADD COLUMN model_config JSON DEFAULT NULL AFTER tools;
-- UPDATE ai_agent SET model_config = JSON_OBJECT(
--     'model', NULLIF(model, ''),
--     'temperature', temperature,
--     'max_tokens', NULLIF(max_tokens, 0),
--     'top_p', top_p);
-- ALTER TABLE ai_agent
--     DROP COLUMN model,
--     DROP COLUMN temperature,
--     DROP COLUMN max_tokens,
--     DROP COLUMN top_p;
