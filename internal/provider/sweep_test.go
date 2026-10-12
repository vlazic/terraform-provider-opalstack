package provider

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

// sweepable is the shape of a name the acceptance tests make: the prefix, six
// random characters, and then the end of the name or a separator. Anything else
// that merely starts with the prefix is left alone.
var sweepable = regexp.MustCompile(`^` + accPrefix + `[a-z0-9]{6}($|[.-])`)

// TestSweep deletes what a failed acceptance test may have left on the account:
// objects whose name has the shape of a test name, and for domains only those
// under the base domain for tests. It runs only with OPALSTACK_SWEEP=1 (make sweep).
//
// The client it uses is configured with the safety prefix as well. Order
// matters: a site before its apps and domains, an app before its shell user.
func TestSweep(t *testing.T) {
	if os.Getenv("OPALSTACK_SWEEP") == "" {
		t.Skip("set OPALSTACK_SWEEP=1 to delete leftover test objects")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*defaultTimeout)
	defer cancel()
	c := accClient(t)
	base := "." + accEnv(t, "OPALSTACK_TEST_DOMAIN_BASE")
	if base == "." {
		t.Fatal("OPALSTACK_TEST_DOMAIN_BASE must be set to sweep")
	}

	sweep(ctx, t, "site", c.Sites(), func(s client.Site) (string, string) { return s.Name, s.ID })
	sweep(ctx, t, "app", c.Apps(), func(a client.App) (string, string) { return a.Name, a.ID })
	// Deleting a domain takes its DNS records with it.
	sweep(ctx, t, "domain", c.Domains(), func(d client.Domain) (string, string) {
		if !strings.HasSuffix(d.Name, base) {
			return "", d.ID // not under the base domain: never swept
		}
		return d.Name, d.ID
	})
	sweep(ctx, t, "shell user", c.OSUsers(), func(u client.OSUser) (string, string) { return u.Name, u.ID })
}

func sweep[T any, K interface {
	List(context.Context) ([]T, error)
	Delete(context.Context, string) error
	WaitDeleted(context.Context, string) error
}](ctx context.Context, t *testing.T, what string, k K, nameAndID func(T) (string, string)) {
	t.Helper()
	all, err := k.List(ctx)
	if err != nil {
		t.Errorf("cannot list the %ss: %v", what, err)
		return
	}
	swept := 0
	for _, o := range all {
		name, id := nameAndID(o)
		if !sweepable.MatchString(name) {
			continue
		}
		if err := k.Delete(ctx, id); err != nil {
			t.Errorf("cannot delete a leftover %s: %v", what, err)
			continue
		}
		if err := k.WaitDeleted(ctx, id); err != nil {
			t.Errorf("a leftover %s was not deleted in time: %v", what, err)
			continue
		}
		swept++
	}
	t.Logf("FINDING sweep: %d leftover %ss deleted", swept, what)
}
