package library

import "context"

type grantedIDsKey struct{}

// GrantedIDsContext is the context key for []string of library IDs the
// caller may see. Missing or empty/nil means list all (admin / unfiltered).
var GrantedIDsContext grantedIDsKey

type userIDKey struct{}

// UserIDContext is an optional context key for a principal user id (string).
// Used for next-episode progress; library does not import auth.
var UserIDContext userIDKey

type grantedBox struct{ IDs []string }

func WithGrantedIDs(ctx context.Context, ids []string) context.Context {
	if ids == nil {
		ids = []string{}
	}
	return context.WithValue(ctx, GrantedIDsContext, grantedBox{IDs: ids})
}

func GrantedIDsFrom(ctx context.Context) []string {
	if box, ok := ctx.Value(GrantedIDsContext).(grantedBox); ok {
		return box.IDs
	}
	ids, _ := ctx.Value(GrantedIDsContext).([]string)
	return ids
}

func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, UserIDContext, userID)
}

func UserIDFrom(ctx context.Context) string {
	s, _ := ctx.Value(UserIDContext).(string)
	return s
}

// grantedFilter returns library IDs to restrict queries to.
// nil means no filter (list all). A non-empty slice filters.
func grantedFilter(ctx context.Context, grantedIDs []string) []string {
	if grantedIDs != nil {
		return grantedIDs
	}
	if _, ok := ctx.Value(GrantedIDsContext).(grantedBox); ok {
		return GrantedIDsFrom(ctx)
	}
	if ids, ok := ctx.Value(GrantedIDsContext).([]string); ok {
		return ids
	}
	return nil
}
