ALTER TABLE devhud_settings
    ALTER COLUMN content_sha256 SET NOT NULL,
    ADD CONSTRAINT devhud_settings_content_sha256_length
        CHECK (octet_length(content_sha256) = 32);
