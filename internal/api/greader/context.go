package greader

import (
	"context"
	"reader/internal/db"
)

type contextKey string

const userCtxKey contextKey = "greader_user"

func SetUserContext(ctx context.Context, user *db.User) context.Context {
	return context.WithValue(ctx, userCtxKey, user)
}

func GetUserFromContext(ctx context.Context) *db.User {
	if val, ok := ctx.Value(userCtxKey).(*db.User); ok {
		return val
	}
	return nil
}

