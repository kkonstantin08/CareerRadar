CREATE TABLE IF NOT EXISTS job_aliases (
 alias_url TEXT PRIMARY KEY,
 job_id BIGINT REFERENCES jobs(id) ON DELETE CASCADE
);
-- NULL preserves an ambiguous identity without assigning it to either existing card.
ALTER TABLE job_aliases ALTER COLUMN job_id DROP NOT NULL;
CREATE INDEX IF NOT EXISTS job_aliases_job_idx ON job_aliases(job_id);
INSERT INTO job_aliases(alias_url,job_id) SELECT url,id FROM jobs ON CONFLICT DO NOTHING;
