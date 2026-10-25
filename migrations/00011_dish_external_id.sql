-- +goose Up
ALTER TABLE dishes ADD COLUMN establishment_id BIGINT;
UPDATE dishes AS d
SET establishment_id = c.establishment_id
FROM menu_categories AS c
WHERE c.id = d.category_id;
ALTER TABLE dishes ALTER COLUMN establishment_id SET NOT NULL;
ALTER TABLE dishes ADD COLUMN external_id TEXT COLLATE "C";
ALTER TABLE menu_categories ADD CONSTRAINT menu_categories_id_establishment_key
    UNIQUE (id, establishment_id);
ALTER TABLE dishes ADD CONSTRAINT dishes_category_establishment_fk
    FOREIGN KEY (category_id, establishment_id)
    REFERENCES menu_categories (id, establishment_id) ON DELETE RESTRICT;
ALTER TABLE dishes ADD CONSTRAINT dishes_external_id_format_check
    CHECK (external_id IS NULL OR external_id ~ '^[A-Za-z0-9._:-]{1,128}$');
ALTER TABLE dishes ADD CONSTRAINT dishes_establishment_external_id_key
    UNIQUE (establishment_id, external_id);

-- +goose Down
-- External mappings are lost; dishes, categories and order snapshots survive.
ALTER TABLE dishes DROP CONSTRAINT dishes_category_establishment_fk;
ALTER TABLE menu_categories DROP CONSTRAINT menu_categories_id_establishment_key;
ALTER TABLE dishes DROP COLUMN external_id;
ALTER TABLE dishes DROP COLUMN establishment_id;
