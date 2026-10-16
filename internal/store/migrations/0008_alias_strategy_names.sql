-- round-robin was a uniform random pick: keep that behaviour under its new name. '' was ordered fallback.
UPDATE aliases SET strategy = 'random' WHERE strategy = 'round-robin';
UPDATE aliases SET strategy = 'priority' WHERE strategy = '';
