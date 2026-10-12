package provider

import (
	"context"
	"maps"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

func configure(t *testing.T, values map[string]tftypes.Value) *provider.ConfigureResponse {
	t.Helper()
	ctx := context.Background()
	p := New("test")()

	var schemaResp provider.SchemaResponse
	p.Schema(ctx, provider.SchemaRequest{}, &schemaResp)
	if diags := schemaResp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("invalid provider schema: %v", diags)
	}

	objType := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	full := map[string]tftypes.Value{}
	for name := range objType.AttributeTypes {
		full[name] = tftypes.NewValue(tftypes.String, nil)
	}
	maps.Copy(full, values)

	var resp provider.ConfigureResponse
	p.Configure(ctx, provider.ConfigureRequest{
		Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: tftypes.NewValue(objType, full)},
	}, &resp)
	return &resp
}

func str(s string) tftypes.Value { return tftypes.NewValue(tftypes.String, s) }

func TestConfigure(t *testing.T) {
	const token = "1111111111111111111111111111111111111111"

	t.Run("token from the attribute", func(t *testing.T) {
		t.Setenv(tokenEnvVar, "")
		resp := configure(t, map[string]tftypes.Value{"token": str(token), "request_timeout": str("45s")})
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if _, ok := resp.ResourceData.(*client.Client); !ok {
			t.Errorf("ResourceData is %T, want *client.Client", resp.ResourceData)
		}
	})

	t.Run("token from the environment", func(t *testing.T) {
		t.Setenv(tokenEnvVar, token)
		if resp := configure(t, nil); resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
	})

	t.Run("missing or blank token", func(t *testing.T) {
		t.Setenv(tokenEnvVar, "")
		for _, values := range []map[string]tftypes.Value{nil, {"token": str("  ")}} {
			if resp := configure(t, values); !resp.Diagnostics.HasError() {
				t.Fatal("expected an error")
			}
		}
	})

	t.Run("unknown token", func(t *testing.T) {
		t.Setenv(tokenEnvVar, token)
		resp := configure(t, map[string]tftypes.Value{"token": tftypes.NewValue(tftypes.String, tftypes.UnknownValue)})
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected an error")
		}
	})

	t.Run("bad request_timeout", func(t *testing.T) {
		for _, bad := range []string{"soon", "-5s", "0s"} {
			resp := configure(t, map[string]tftypes.Value{"token": str(token), "request_timeout": str(bad)})
			if !resp.Diagnostics.HasError() {
				t.Errorf("request_timeout %q: expected an error", bad)
			}
		}
	})
}
