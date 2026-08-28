package users

import "context"

type Prefs struct {
	AudioLang    string `json:"audio_lang"`
	SubtitleLang string `json:"subtitle_lang"`
	SubtitleMode string `json:"subtitle_mode"`
	Autoplay     bool   `json:"autoplay"`
}

type PrefsStore interface {
	Get(ctx context.Context, userID string) (Prefs, error)
	Set(ctx context.Context, userID string, p Prefs) error
}
