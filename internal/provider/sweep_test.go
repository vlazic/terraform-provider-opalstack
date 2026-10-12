package provider

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

// TestSweep deletes every object a failed acceptance test may have left on the
// account: those whose name starts with accPrefix, and the DNS records of such
// domains. It runs only with OPALSTACK_SWEEP=1 (make sweep).
//
// The match is on the prefix and nothing else, and the client it uses is
// configured with the same prefix, so a wrong match would be refused there too.
// Order matters: a site before its apps and domains, an app before its shell user.
func TestSweep(t *testing.T) {
	if os.Getenv("OPALSTACK_SWEEP") == "" {
		t.Skip("set OPALSTACK_SWEEP=1 to delete leftover test objects")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*defaultTimeout)
	defer cancel()
	c := accClient(t)

	sweep(ctx, t, "site", c.Sites(), func(s client.Site) (string, string) { return s.Name, s.ID })
	sweep(ctx, t, "app", c.Apps(), func(a client.App) (string, string) { return a.Name, a.ID })
	// Deleting a domain takes its DNS records with it.
	sweep(ctx, t, "domain", c.Domains(), func(d client.Domain) (string, string) { return d.Name, d.ID })
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
		if !strings.HasPrefix(name, accPrefix) {
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
