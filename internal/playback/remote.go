package playback

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/decision"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/library"
)

// SourceLocal is the playback source id for files in local libraries.
const SourceLocal = "local"

// SourceOption is one selectable playback source for an item.
type SourceOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// RemoteStream is a stream served by an external media source.
type RemoteStream struct {
	Source     string
	Delivery   string // decision.DeliveryDirect or decision.DeliveryHLS
	URL        string
	DurationMS int64
	// Qualities are the quality choices the source can serve, auto first.
	Qualities []string
	// Stop revokes the stream grant and ends any remote transcode.
	Stop func()
}

// ErrSourceUnavailable means the chosen external source cannot play now.
var ErrSourceUnavailable = errors.New("source_unavailable")

// SourceUnavailable is a source failure whose Reason is safe to show to the viewer.
type SourceUnavailable struct{ Reason string }

func (e *SourceUnavailable) Error() string {
	if e != nil && e.Reason != "" {
		return e.Reason
	}
	return ErrSourceUnavailable.Error()
}

func (e *SourceUnavailable) Unwrap() error { return ErrSourceUnavailable }

// Sources resolves external media sources for catalogue items. pick is ""
// (prefer local), SourceLocal, or an option id. quality is a player quality
// choice ("" or "auto" for the original). A nil stream plays locally.
type Sources interface {
	Resolve(ctx context.Context, itemKind, itemID, pick, quality string, hasLocal bool) (*RemoteStream, []SourceOption, error)
}

// createRemote answers the request with an external source session when one
// applies. It returns false, with the item's source options, to continue
// with local playback.
func (a *API) createRemote(w http.ResponseWriter, r *http.Request, p *auth.Principal, body createBody) (bool, []SourceOption) {
	if a.Sources == nil || !p.IsUser() || body.MediaFileID != "" {
		return false, nil
	}
	hasLocal := false
	if a.Locator != nil {
		if loc, err := a.Locator.LocateItem(r.Context(), body.ItemKind, body.ItemID); err == nil && loc != nil && loc.AbsPath != "" {
			_, statErr := os.Stat(loc.AbsPath)
			hasLocal = statErr == nil
		}
	}
	if !a.remoteAllowed(r.Context(), p, body.ItemKind, body.ItemID) {
		writeHidden(w, errHidden)
		return true, nil
	}
	stream, options, err := a.Sources.Resolve(r.Context(), body.ItemKind, body.ItemID, body.Source, body.Quality, hasLocal)
	if err != nil {
		if errors.Is(err, library.ErrNotFound) {
			writeHidden(w, errHidden)
		} else {
			msg := "The media source is unavailable right now."
			var why *SourceUnavailable
			if errors.As(err, &why) && why.Reason != "" {
				msg = why.Reason
			}
			if a.Log != nil {
				a.Log.Warn("remote playback unavailable", "category", "playback", "item", body.ItemKind+"/"+body.ItemID, "source", body.Source, "err", err.Error())
			}
			httpapi.WriteErr(w, http.StatusServiceUnavailable, "source_unavailable", msg)
		}
		return true, nil
	}
	if stream == nil {
		return false, options
	}

	a.slotMu.Lock()
	a.supersedePlayback(p, body.ItemKind, body.ItemID, body.ReplaceSessionID)
	a.slotMu.Unlock()
	var start int64
	if body.StartMS != nil {
		if *body.StartMS > 0 {
			start = *body.StartMS
		}
	} else if a.Progress != nil {
		if rec, err := a.Progress.Get(r.Context(), p.UserID, body.ItemKind, body.ItemID); err == nil {
			start = rec.ResumeMS
		}
	}
	owner := *p
	sess := &Session{
		ID: uuid.NewString(), Kind: p.Kind, UserID: p.UserID, Owner: &owner,
		ItemKind: body.ItemKind, ItemID: body.ItemID,
		Delivery: stream.Delivery, Mode: "remote", Quality: body.Quality,
		StartMS: start, ResumeMS: start, DurationMS: stream.DurationMS,
		Client: body.Client, Created: time.Now(), LastPing: time.Now(),
		SeekableFromMS: start, Intro: a.intro(r.Context(), body.ItemKind, body.ItemID),
		NextEpisode: a.nextEpisode(r.Context(), body.ItemKind, body.ItemID),
		Decision:    decision.Result{Delivery: stream.Delivery, Mode: "remote", Playback: "remote"},
		Source:      stream.Source, SourceOptions: options, RemoteURL: stream.URL, RemoteQualities: stream.Qualities, remoteStop: stream.Stop,
	}
	a.Reg.Put(sess)
	if a.Log != nil {
		a.Log.Info("playback session", "category", "playback", "id", sess.ID, "mode", "remote",
			"delivery", sess.Delivery, "item", sess.ItemKind+"/"+sess.ItemID, "source", sess.Source, "start_ms", start)
	}
	httpapi.WriteJSON(w, 200, a.sessionJSON(sess))
	return true, nil
}

// remoteAllowed applies the same account checks as local playback: party
// only accounts need party access, others need library read access and a
// permitted content rating.
func (a *API) remoteAllowed(ctx context.Context, p *auth.Principal, itemKind, itemID string) bool {
	if a.DB == nil {
		return false
	}
	libID, _, err := library.ItemRating(ctx, a.DB, itemKind, itemID)
	if err != nil {
		return false
	}
	if ok, err := library.ItemPermitted(ctx, a.DB, p.UserID, itemKind, itemID); err != nil || !ok {
		return false
	}
	if p.PartyOnly && !partyAccessGranted(ctx) {
		return a.partyAccess(p.ID(), itemKind, itemID)
	}
	return p.IsAdmin || a.Grants == nil || a.Grants.CanRead(ctx, p.UserID, libID)
}
