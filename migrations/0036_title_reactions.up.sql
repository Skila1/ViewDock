-- A viewer's like (1) or dislike (-1) of a movie or series. Recommendations
-- weigh genres by these, and disliked titles are never recommended.
CREATE TABLE title_reactions (
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_kind  TEXT NOT NULL,
    item_id    TEXT NOT NULL,
    reaction   INTEGER NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, item_kind, item_id)
);
