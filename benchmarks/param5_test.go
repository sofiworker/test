package benchmarks

import (
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	web "example.com/web"
)

var param5Keys = []string{"org", "team", "member", "role", "perm"}

func newWebParam5() *web.App {
	app := web.New()
	app.Must(web.GetText("/orgs/{org}/teams/{team}/members/{member}/roles/{role}/perms/{perm}",
		web.InFunc(func(r web.Req) (string, error) {
			var b strings.Builder
			for _, k := range param5Keys {
				v, err := r.Path().String(k)
				if err != nil {
					return "", err
				}
				b.WriteString(v)
			}
			return b.String(), nil
		}),
		func(s string) (string, error) { return s, nil }))
	return app
}

func newGinParam5() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.GET("/orgs/:org/teams/:team/members/:member/roles/:role/perms/:perm", func(c *gin.Context) {
		c.String(200, c.Param("org")+c.Param("team")+c.Param("member")+c.Param("role")+c.Param("perm"))
	})
	return r
}

func BenchmarkWebParam5(b *testing.B) {
	bench(b, newWebParam5(), "/orgs/o-1/teams/t-2/members/m-3/roles/r-4/perms/p-5")
}

func BenchmarkGinParam5(b *testing.B) {
	bench(b, newGinParam5(), "/orgs/o-1/teams/t-2/members/m-3/roles/r-4/perms/p-5")
}
