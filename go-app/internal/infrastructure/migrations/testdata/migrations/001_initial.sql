-- +goose Up
CREATE TABLE test (id INTEGER PRIMARY KEY);

-- +goose Down
DROP TABLE test;
