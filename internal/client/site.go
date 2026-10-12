package client

import "encoding/json"

// Route sends requests for a URI prefix of a site to an app. ID is assigned by
// the API and is never sent.
type Route struct {
	ID  string `json:"id,omitempty"`
	App string `json:"app"`
	URI string `json:"uri,omitempty"`
}

type Site struct {
	Status
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Server        string   `json:"server"`
	IP4           *string  `json:"ip4"`
	IP6           *string  `json:"ip6"`
	Domains       []string `json:"domains"`
	Routes        []Route  `json:"routes"`
	Cert          *string  `json:"cert"`
	PrimaryDomain *string  `json:"primary_domain"`
	Redirect      bool     `json:"redirect"`
	GenerateLE    bool     `json:"generate_le"`
	Disabled      bool     `json:"disabled"`
}

// SiteCreate always sends domains and routes as arrays, never as null: the API
// requires both.
type SiteCreate struct {
	Name          string   `json:"name,omitempty"`
	IP4           *string  `json:"ip4,omitempty"`
	IP6           *string  `json:"ip6,omitempty"`
	Domains       []string `json:"domains"`
	Routes        []Route  `json:"routes"`
	Cert          *string  `json:"cert,omitempty"`
	PrimaryDomain *string  `json:"primary_domain,omitempty"`
	Redirect      *bool    `json:"redirect,omitempty"`
	GenerateLE    *bool    `json:"generate_le,omitempty"`
	Disabled      *bool    `json:"disabled,omitempty"`
}

// SiteUpdate sends only the fields that are set. For Domains and Routes, nil
// means unchanged and an empty slice replaces the list with nothing; Routes
// always replaces the site's whole route list. The four Nullable fields can be
// cleared with Null.
type SiteUpdate struct {
	ID            string           `json:"id"`
	Name          string           `json:"name,omitempty"`
	IP4           Nullable[string] `json:"ip4,omitzero"`
	IP6           Nullable[string] `json:"ip6,omitzero"`
	Domains       []string         `json:"domains,omitzero"`
	Routes        []Route          `json:"routes,omitzero"`
	Cert          Nullable[string] `json:"cert,omitzero"`
	PrimaryDomain Nullable[string] `json:"primary_domain,omitzero"`
	Redirect      *bool            `json:"redirect,omitempty"`
	GenerateLE    *bool            `json:"generate_le,omitempty"`
	Disabled      *bool            `json:"disabled,omitempty"`
}

func (r SiteCreate) MarshalJSON() ([]byte, error) {
	type plain SiteCreate // drops this method, so the call below does not recurse
	if r.Domains == nil {
		r.Domains = []string{}
	}
	if r.Routes == nil {
		r.Routes = []Route{}
	}
	return json.Marshal(plain(r))
}

func (c *Client) Sites() Kind[Site, SiteCreate, SiteUpdate] {
	k := kind[Site, SiteCreate, SiteUpdate](c, "site")
	k.refs = []ref{{path: "domains", kind: "domain"}, {path: "routes.app", kind: "app"}}
	return k
}
