-- How an alias picks the first member a request tries: '' tries them in order, 'round-robin' spreads requests.
ALTER TABLE aliases ADD COLUMN strategy TEXT NOT NULL DEFAULT '';
