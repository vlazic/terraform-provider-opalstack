package client

const kindDomain = "domain"

// Domain deliberately has no field for dkim_privkey. The API returns that
// private key in every domain response, and nothing here should carry it.
type Domain struct {
	ID               string  `json:"id"`
	State            string  `json:"state"`
	Ready            bool    `json:"ready"`
	Name             string  `json:"name"`
	DelegatedDNSZone bool    `json:"delegated_dns_zone"`
	IsValidHostname  bool    `json:"is_valid_hostname"`
	DKIMRecord       *string `json:"dkim_record"`
	DKIMPubkey       *string `json:"dkim_pubkey"`
}

type DomainCreate struct {
	Name             string `json:"name"`
	DelegatedDNSZone *bool  `json:"delegated_dns_zone,omitempty"`
}

func (r DomainCreate) guardTarget() guardTarget { return guardTarget{name: r.Name} }

// DomainUpdate can change delegated_dns_zone and nothing else. A domain cannot
// be renamed.
type DomainUpdate struct {
	ID               string `json:"id"`
	DelegatedDNSZone *bool  `json:"delegated_dns_zone,omitempty"`
}

func (r DomainUpdate) objectID() string { return r.ID }

func (c *Client) Domains() Kind[Domain, DomainCreate, DomainUpdate] {
	return Kind[Domain, DomainCreate, DomainUpdate]{c: c, name: kindDomain}
}
