package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/iudanet/architecture-bionicpro/backend/internal/auth"
	"github.com/iudanet/architecture-bionicpro/backend/internal/authtest"
)

func TestVerifier_Verify(t *testing.T) {
	iss := authtest.New(t)
	v := auth.NewVerifier(authtest.Issuer, iss.KeySet(), "reports-api")

	t.Run("valid token", func(t *testing.T) {
		p, err := v.Verify(context.Background(), iss.AccessToken("user-1", "reports-api", "prothetic_user"))
		require.NoError(t, err)
		assert.Equal(t, "user-1", p.Subject)
		assert.True(t, p.HasRole("prothetic_user"))
	})

	cases := map[string]func(c map[string]any){
		"expired":        func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() },
		"wrong audience": func(c map[string]any) { c["aud"] = []string{"account"} },
		"wrong issuer":   func(c map[string]any) { c["iss"] = "http://evil.test/realms/reports-realm" },
		"no subject":     func(c map[string]any) { delete(c, "sub") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := authtest.Claims("user-1", "reports-api", "prothetic_user")
			mutate(c)
			_, err := v.Verify(context.Background(), iss.Sign(c))
			assert.ErrorIs(t, err, auth.ErrInvalidToken)
		})
	}

	t.Run("foreign signing key", func(t *testing.T) {
		other := authtest.New(t)
		_, err := v.Verify(context.Background(), other.AccessToken("user-1", "reports-api", "prothetic_user"))
		assert.ErrorIs(t, err, auth.ErrInvalidToken)
	})
}
