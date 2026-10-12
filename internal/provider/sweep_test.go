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

	dbName := func(d client.DB) (string, string) { return d.Name, d.ID }
	dbUserName := func(u client.DBUser) (string, string) { return u.Name, u.ID }
	// An address before the mailbox and the domain it needs. Only addresses on
	// a test domain under the base domain are swept.
	sweep(ctx, t, "address", c.Addresses(), func(a client.Address) (string, string) {
		local, domain, _ := strings.Cut(a.Source, "@")
		if !sweepable.MatchString(domain) || !strings.HasSuffix(domain, base) {
			return "", a.ID
		}
		return local, a.ID
	})
	sweep(ctx, t, "mailbox", c.MailUsers(), func(u client.MailUser) (string, string) { return u.Name, u.ID })
	sweep(ctx, t, "MariaDB database", c.MariaDBs(), dbName)
	sweep(ctx, t, "MariaDB user", c.MariaUsers(), dbUserName)
	sweep(ctx, t, "PostgreSQL database", c.PsqlDBs(), dbName)
	sweep(ctx, t, "PostgreSQL user", c.PsqlUsers(), dbUserName)
	sweep(ctx, t, "site", c.Sites(), func(s client.Site) (string, string) { return s.Name, s.ID })
	sweep(ctx, t, "certificate", c.Certs(), func(c client.Cert) (string, string) { return c.Name, c.ID })
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

// sweep deletes the leftovers of one kind, as a subtest of its own. A kind the
// token cannot list fails the sweep: unseen is not clean. Listing needs only
// the read switch of the kind, which the test token keeps on.
func sweep[T any, K interface {
	deleter
	List(context.Context) ([]T, error)
}](ctx context.Context, t *testing.T, what string, k K, nameAndID func(T) (string, string)) {
	t.Helper()
	t.Run(what, func(t *testing.T) {
		all, err := k.List(ctx)
		if err != nil {
			t.Fatalf("cannot list the %s: %v", plural(what), err)
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
		t.Logf("FINDING sweep: %d leftover %s deleted", swept, plural(what))
	})
}
