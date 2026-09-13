CREATE TABLE links
(id SERIAL PRIMARY KEY,
code VARCHAR(7) UNIQUE,
long_url TEXT,
created_at TIMESTAMP DEFAULT NOW(),
hit_count BIGINT DEFAULT 0);

CREATE UNIQUE INDEX links_long_url_idx ON links (long_url);
