package client

const kindSite = "site"

// Route sends requests for a URI prefix of a site to an app. ID is assigned by
// the API and is never sent.
type Route struct {
	ID  string `json:"id,omitempty"`
	App string `json:"app"`
	URI string `json:"uri"`
}

type Site struct {
	ID            string   `json:"id"`
	State         string   `json:"state"`
	Ready         bool     `json:"ready"`
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

type SiteCreate struct {
	Name          string   `json:"name"`
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

func (r SiteCreate) guardTarget() guardTarget { return guardTarget{name: r.Name} }

// SiteUpdate sends only the fields that are set. Routes, when set, replace the
// site's whole route list; a pointer to an empty slice clears it.
type SiteUpdate struct {
	ID            string    `json:"id"`
	Name          string    `json:"name,omitempty"`
	IP4           *string   `json:"ip4,omitempty"`
	IP6           *string   `json:"ip6,omitempty"`
	Domains       *[]string `json:"domains,omitempty"`
	Routes        *[]Route  `json:"routes,omitempty"`
	Cert          *string   `json:"cert,omitempty"`
	PrimaryDomain *string   `json:"primary_domain,omitempty"`
	Redirect      *bool     `json:"redirect,omitempty"`
	GenerateLE    *bool     `json:"generate_le,omitempty"`
	Disabled      *bool     `json:"disabled,omitempty"`
}

func (r SiteUpdate) objectID() string { return r.ID }

func (c *Client) Sites() Kind[Site, SiteCreate, SiteUpdate] {
	return Kind[Site, SiteCreate, SiteUpdate]{c: c, name: kindSite}
}
