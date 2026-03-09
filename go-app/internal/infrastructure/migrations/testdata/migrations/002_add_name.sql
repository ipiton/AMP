-- +goose Up
ALTER TABLE test ADD COLUMN name TEXT;

-- +goose Down
ALTER TABLE test DROP COLUMN name;
