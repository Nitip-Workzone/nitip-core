-- +goose Up
-- +goose StatementBegin
CREATE TABLE trips (
    id UUID PRIMARY KEY,
    runner_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    origin_name TEXT NOT NULL,
    origin_lat DOUBLE PRECISION NOT NULL,
    origin_lng DOUBLE PRECISION NOT NULL,
    destination_name TEXT NOT NULL,
    destination_lat DOUBLE PRECISION NOT NULL,
    destination_lng DOUBLE PRECISION NOT NULL,
    departure_time TIMESTAMP WITH TIME ZONE NOT NULL,
    return_time TIMESTAMP WITH TIME ZONE NULL,
    status VARCHAR(30) NOT NULL DEFAULT 'active', -- active, completed, cancelled
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_trips_runner_id ON trips(runner_id);
CREATE INDEX idx_trips_status ON trips(status);
CREATE INDEX idx_trips_origin ON trips(origin_lat, origin_lng);
CREATE INDEX idx_trips_destination ON trips(destination_lat, destination_lng);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS trips;
-- +goose StatementEnd
