package users

import (
	"context"
	"database/sql"
)

type PrefsDB struct{ DB *sql.DB }

func NewPrefs(db *sql.DB) *PrefsDB { return &PrefsDB{DB: db} }

func (p *PrefsDB) Get(ctx context.Context, userID string) (Prefs, error) {
	var out Prefs
	var auto int
	err := p.DB.QueryRowContext(ctx, `
		SELECT audio_lang, subtitle_lang, subtitle_mode, autoplay FROM user_preferences WHERE user_id = ?
	`, userID).Scan(&out.AudioLang, &out.SubtitleLang, &out.SubtitleMode, &auto)
	if err == sql.ErrNoRows {
		return Prefs{SubtitleMode: "auto", Autoplay: true}, nil
	}
	out.Autoplay = auto == 1
	return out, err
}

func (p *PrefsDB) Set(ctx context.Context, userID string, pref Prefs) error {
	if pref.SubtitleMode == "" {
		pref.SubtitleMode = "auto"
	}
	a := 0
	if pref.Autoplay {
		a = 1
	}
	_, err := p.DB.ExecContext(ctx, `
		INSERT INTO user_preferences(user_id, audio_lang, subtitle_lang, subtitle_mode, autoplay)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			audio_lang=excluded.audio_lang,
			subtitle_lang=excluded.subtitle_lang,
			subtitle_mode=excluded.subtitle_mode,
			autoplay=excluded.autoplay
	`, userID, pref.AudioLang, pref.SubtitleLang, pref.SubtitleMode, a)
	return err
}
