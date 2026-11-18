CREATE TABLE cooling (
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    until_ns INTEGER NOT NULL,
    reason TEXT NOT NULL,
    backoff_ns INTEGER NOT NULL,
    PRIMARY KEY (provider, model)
);
