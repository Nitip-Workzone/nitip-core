-- +goose Up
-- +goose StatementBegin
-- Enable PostGIS extension
CREATE EXTENSION IF NOT EXISTS postgis;

-- Create a functional GIST index for trips based on Origin coordinates
-- Using geography type for accurate distance measurements in meters
CREATE INDEX trips_origin_spatial_idx ON trips USING GIST (
    CAST(ST_SetSRID(ST_MakePoint(origin_lng, origin_lat), 4326) AS geography)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS trips_origin_spatial_idx;
-- +goose StatementEnd
