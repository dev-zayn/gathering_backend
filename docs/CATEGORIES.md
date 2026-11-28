# The store's categories

How the store groups games. Until 2026-11-24 the list was closed and written
into the code (speed, brain, words, luck, guess, draw, party, other), and the
app carried each one's words. Now it is a table an admin keeps from the
console: a category is a key the games carry and its name in each language
the app speaks, in the order the store shows them.

## Storage (migration `00017_categories.sql`)

```
game_categories
  id         varchar(32)  primary key   the key a game and a build carry
  name_ar    varchar(40)                what the app shows in Arabic
  name_en    varchar(40)                and in English
  position   integer                    the store's order, smallest first; ties by id
  created_at, updated_at timestamptz
```

The migration makes the eight that were, with the app's words for them and
positions 10 to 80, `other` at 1000. `games.category` and
`game_builds.category` reference `game_categories (id)`: a game can only
carry a category that exists (a row with any other value is moved to
`other` first).

A key is 2 to 32 characters of `a-z`, `0-9` and `-`, starting with a
letter, and never changes once made: the games carry it. The names may
change at any time.

`other` is where a game goes when nothing else fits, and where the games of
a deleted category go: it can be renamed and moved, never deleted.

## A category, as the routes carry it

`{ id, name_ar, name_en, position, games }`: `games` is how many games carry
it, in any listing (the console's; the player's and the publisher's routes
leave it out).

## The admin's routes (scope `admin`; every change needs the `admin` role)

### `admin-categories`: `GET /v1/admin/categories`

**200** `{ categories: [id], items: [category] }`, in the store's order.
`categories` is the keys alone, as before.

### `admin-category-create`: `POST /v1/admin/categories`

`{ id, name_ar, name_en, position? }`. **201** the category. Without a
position it goes after every category but `other` (the largest position
under `other`'s, plus 10).

Refusals, in the order they are checked:

| Status | Code | When |
| --- | --- | --- |
| 422 | `invalid_fields` | `id: required`, `id: invalid` (not 2 to 32 of `a-z0-9-` from a letter); `name_ar: required`, `name_ar: too_long` (40); the same for `name_en`; `position: 0_to_10000` |
| 409 | `category_exists` | the key is taken |

Audit `category.created` (area `games`, subject the key, `{name_ar, name_en,
position}`).

### `admin-category-edit`: `PATCH /v1/admin/categories/{category}`

`{ name_ar?, name_en?, position? }`: only what is sent is written. **200**
the category.

| Status | Code | When |
| --- | --- | --- |
| 404 | `not_found` | no such category |
| 422 | `invalid_fields` | `name_ar: required` (sent empty), `name_ar: too_long`; the same for `name_en`; `position: 0_to_10000` |

Audit `category.edited` (subject the key, `{fields}`: which were sent).

### `admin-category-delete`: `DELETE /v1/admin/categories/{category}`

Every game and every build that carries it moves to `other`, and the
category goes, in one transaction. **200** `{ moved }`: how many games
moved.

| Status | Code | When |
| --- | --- | --- |
| 404 | `not_found` | no such category |
| 409 | `category_protected` | it is `other` |

Audit `category.deleted` (subject the key, `{moved}`).

## Where else the list is read

- `GET /v1/categories` (`categories`, scope `session`, the player's):
  **200** `{ items: [{id, name_ar, name_en}] }`, in the store's order. The
  app names its category keys with these, in the language it is showing; a
  key it has no row for is shown as `other`.
- `GET /v1/games?category=` takes any key in the table; another is `422`
  `category: unknown`, as before.
- `GET /v1/publisher/categories` (`publisher-categories`): **200**
  `{ categories: [id], items: [{id, name_ar, name_en}] }`, for the studio's
  picker.
- A category in `admin-game-listing` and in a publisher's build edit must be
  in the table: `422` `category: unknown`, as before.

## The console

**Categories** (`/categories`, under Games in the rail): the list in the
store's order with each one's key, names, position and how many games carry
it; "New category" (a dialog: the key, the two names, an optional
position); a row's "Edit" (the names and the position; the key is shown,
not editable); "Delete" behind a confirmation that names how many games
move to Other, and no Delete on `other`. A viewer without the `admin` role
sees the list without the buttons. Every picker and every label of a
category in the console (a game's store copy, a build, the studio's game)
reads the names from here.

## The app

The Games tab's category keys and every label of a category read
`GET /v1/categories` (with the store's list, and again on a pull), in the
table's order; a category no game carries has no key. Until that answer
arrives, or when it fails, the app falls back to the eight it knows. A
version of the app from before this reads only the eight, and shows any
other key as "أخرى".
