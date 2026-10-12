package client

// Domain deliberately has no field for dkim_privkey. The API returns that
// private key in every domain response, and nothing here should carry it.
type Domain struct {
	Status
	ID               string  `json:"id"`
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

// DomainUpdate can change delegated_dns_zone and nothing else. A domain cannot
// be renamed.
type DomainUpdate struct {
	ID               string `json:"id"`
	DelegatedDNSZone *bool  `json:"delegated_dns_zone,omitempty"`
}

func (c *Client) Domains() Kind[Domain, DomainCreate, DomainUpdate] {
	return kind[Domain, DomainCreate, DomainUpdate](c, "domain")
}
