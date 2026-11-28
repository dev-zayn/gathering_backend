-- +goose Up

-- The store's categories (docs/CATEGORIES.md): until now a closed list in the
-- code, with the app carrying their words. Now an admin keeps them.
CREATE TABLE game_categories (
    id         varchar(32) PRIMARY KEY,              -- the key a game and a build carry; never changes
    name_ar    varchar(40) NOT NULL,                 -- what the app shows in Arabic
    name_en    varchar(40) NOT NULL,                 -- and in English
    position   integer     NOT NULL DEFAULT 0,       -- the store's order, smallest first; ties by id
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX game_categories_order ON game_categories (position, id);

-- The eight there were, with the app's words for them. Other is last: it is
-- where a new game waits, and where a deleted category's games go.
INSERT INTO game_categories (id, name_ar, name_en, position) VALUES
    ('speed', 'سرعة',  'Speed',    10),
    ('brain', 'ذكاء',  'Brain',    20),
    ('words', 'كلمات', 'Words',    30),
    ('luck',  'حظ',    'Luck',     40),
    ('guess', 'تخمين', 'Guessing', 50),
    ('draw',  'رسم',   'Drawing',  60),
    ('party', 'حفلة',  'Party',    70),
    ('other', 'أخرى',  'Other',    1000);

-- A game or a build can only carry a category that exists.
UPDATE games SET category = 'other' WHERE category NOT IN (SELECT id FROM game_categories);
UPDATE game_builds SET category = 'other' WHERE category NOT IN (SELECT id FROM game_categories);
ALTER TABLE games ADD CONSTRAINT games_category_fkey
    FOREIGN KEY (category) REFERENCES game_categories (id);
ALTER TABLE game_builds ADD CONSTRAINT game_builds_category_fkey
    FOREIGN KEY (category) REFERENCES game_categories (id);
CREATE INDEX game_builds_category ON game_builds (category);

-- +goose Down
DROP INDEX game_builds_category;
ALTER TABLE game_builds DROP CONSTRAINT game_builds_category_fkey;
ALTER TABLE games DROP CONSTRAINT games_category_fkey;
DROP TABLE game_categories;
