ALTER TABLE apps ADD COLUMN has_events boolean;
UPDATE apps SET has_events = true;
ALTER TABLE apps ALTER COLUMN has_events SET NOT NULL;
