ALTER TABLE service_meter_allowlists
ADD COLUMN background_usage_purpose text;

ALTER TABLE service_meter_allowlists
ADD CONSTRAINT service_meter_allowlists_background_usage_purpose_check
CHECK (
    background_usage_purpose IS NULL
    OR background_usage_purpose = 'realqa_storage'
);
