CREATE TABLE IF NOT EXISTS ice_frames (
    frame_id           BIGINT PRIMARY KEY,
    sweep_id           BIGINT NOT NULL,
    observed_at        TIMESTAMPTZ NOT NULL,
    vessel_lat         DOUBLE PRECISION NOT NULL,
    vessel_lon         DOUBLE PRECISION NOT NULL,
    az_start_deg       DOUBLE PRECISION NOT NULL,
    az_end_deg         DOUBLE PRECISION NOT NULL,
    range_bins         INTEGER NOT NULL,
    bin_spacing_m      DOUBLE PRECISION NOT NULL,
    mean_intensity     DOUBLE PRECISION NOT NULL,
    max_intensity      SMALLINT NOT NULL,
    ice_concentration  DOUBLE PRECISION NOT NULL,
    ridge_count        INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ice_frames_time
    ON ice_frames (observed_at);
CREATE INDEX IF NOT EXISTS idx_ice_frames_geo_time
    ON ice_frames (vessel_lat, vessel_lon, observed_at);

CREATE TABLE IF NOT EXISTS ice_ridges (
    id              BIGSERIAL PRIMARY KEY,
    frame_id        BIGINT NOT NULL REFERENCES ice_frames(frame_id) ON DELETE CASCADE,
    sweep_id        BIGINT NOT NULL,
    ridge_idx       INTEGER NOT NULL,
    observed_at     TIMESTAMPTZ NOT NULL,
    azimuth_deg     DOUBLE PRECISION NOT NULL,
    start_bin       INTEGER NOT NULL,
    end_bin         INTEGER NOT NULL,
    peak_bin        INTEGER NOT NULL,
    range_start_m   DOUBLE PRECISION NOT NULL,
    range_end_m     DOUBLE PRECISION NOT NULL,
    range_peak_m    DOUBLE PRECISION NOT NULL,
    peak_intensity  SMALLINT NOT NULL,
    mean_intensity  DOUBLE PRECISION NOT NULL,
    lat             DOUBLE PRECISION NOT NULL,
    lon             DOUBLE PRECISION NOT NULL,
    UNIQUE (frame_id, ridge_idx)
);

CREATE INDEX IF NOT EXISTS idx_ice_ridges_time
    ON ice_ridges (observed_at);
CREATE INDEX IF NOT EXISTS idx_ice_ridges_geo_time
    ON ice_ridges (lat, lon, observed_at);
