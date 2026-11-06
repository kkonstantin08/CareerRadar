CREATE INDEX IF NOT EXISTS jobs_level_lower_idx ON jobs (lower(level)) WHERE level <> '';
CREATE INDEX IF NOT EXISTS jobs_direction_lower_idx ON jobs (lower(direction)) WHERE direction <> '';
CREATE INDEX IF NOT EXISTS jobs_company_lower_idx ON jobs (lower(company)) WHERE company <> '';
CREATE INDEX IF NOT EXISTS jobs_location_lower_idx ON jobs (lower(location)) WHERE location <> '';
CREATE INDEX IF NOT EXISTS jobs_offer_type_lower_idx ON jobs (lower(offer_type)) WHERE offer_type <> '';
CREATE INDEX IF NOT EXISTS jobs_work_mode_lower_idx ON jobs (lower(work_mode)) WHERE work_mode <> '';
