-- +goose Up
INSERT INTO establishments (name, accepts_orders, api_key_hash)
VALUES (
    'Демо Кафе',
    TRUE,
    decode('5f7b2ed74a9736a538a6fb5a5a1a76afdcdd0d177d7fc6e79c6b6843527d848d', 'hex')
);

INSERT INTO menu_categories (establishment_id, name)
SELECT id, category_name
FROM establishments
CROSS JOIN (VALUES ('Бургеры'), ('Напитки')) AS categories(category_name)
WHERE api_key_hash = decode(
    '5f7b2ed74a9736a538a6fb5a5a1a76afdcdd0d177d7fc6e79c6b6843527d848d',
    'hex'
);

INSERT INTO dishes (category_id, name, description, price_minor, is_available)
SELECT
    c.id,
    dish_name,
    description,
    price_minor,
    TRUE
FROM menu_categories AS c
JOIN establishments AS e ON e.id = c.establishment_id
CROSS JOIN (
    VALUES
        ('Классический бургер', 'Говяжья котлета, овощи и соус', 45900::BIGINT),
        ('Чизбургер', 'Говяжья котлета и двойной сыр', 49900::BIGINT)
) AS demo_dishes(dish_name, description, price_minor)
WHERE c.name = 'Бургеры'
  AND e.api_key_hash = decode(
      '5f7b2ed74a9736a538a6fb5a5a1a76afdcdd0d177d7fc6e79c6b6843527d848d',
      'hex'
  );

INSERT INTO dishes (category_id, name, description, price_minor, is_available)
SELECT c.id, 'Морс', 'Ягодный морс, 500 мл', 12900, TRUE
FROM menu_categories AS c
JOIN establishments AS e ON e.id = c.establishment_id
WHERE c.name = 'Напитки'
  AND e.api_key_hash = decode(
      '5f7b2ed74a9736a538a6fb5a5a1a76afdcdd0d177d7fc6e79c6b6843527d848d',
      'hex'
  );

-- +goose Down
DELETE FROM dishes
WHERE category_id IN (
    SELECT c.id
    FROM menu_categories AS c
    JOIN establishments AS e ON e.id = c.establishment_id
    WHERE e.api_key_hash = decode(
        '5f7b2ed74a9736a538a6fb5a5a1a76afdcdd0d177d7fc6e79c6b6843527d848d',
        'hex'
    )
);

DELETE FROM menu_categories
WHERE establishment_id IN (
    SELECT id
    FROM establishments
    WHERE api_key_hash = decode(
        '5f7b2ed74a9736a538a6fb5a5a1a76afdcdd0d177d7fc6e79c6b6843527d848d',
        'hex'
    )
);

DELETE FROM establishments
WHERE api_key_hash = decode(
    '5f7b2ed74a9736a538a6fb5a5a1a76afdcdd0d177d7fc6e79c6b6843527d848d',
    'hex'
);
