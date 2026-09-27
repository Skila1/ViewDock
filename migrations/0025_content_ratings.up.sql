-- rating_age is the minimum viewer age for a title; NULL means unrated.
-- rating_source: '' (not yet looked up), 'tmdb', or 'admin' (never overwritten by metadata refreshes).
-- Episodes inherit the rating of their series.
ALTER TABLE movies ADD COLUMN content_rating TEXT NOT NULL DEFAULT '';
ALTER TABLE movies ADD COLUMN rating_age INTEGER;
ALTER TABLE movies ADD COLUMN rating_source TEXT NOT NULL DEFAULT '';
ALTER TABLE series ADD COLUMN content_rating TEXT NOT NULL DEFAULT '';
ALTER TABLE series ADD COLUMN rating_age INTEGER;
ALTER TABLE series ADD COLUMN rating_source TEXT NOT NULL DEFAULT '';
-- Administrator-set maximum rating age for an account; 0 means no account-level restriction.
-- The effective restriction is the lowest non-zero value of this and household_members.age_limit.
ALTER TABLE users ADD COLUMN content_age_limit INTEGER NOT NULL DEFAULT 0;
