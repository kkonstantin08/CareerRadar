-- NULL preserves legacy location matching. An empty array records incomplete remote eligibility.
ALTER TABLE jobs ADD COLUMN applicant_locations TEXT[];
