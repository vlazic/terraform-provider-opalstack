package client

// Cert is a TLS certificate. The API returns the private key with every read;
// this type has no field for it, so nothing here carries it.
type Cert struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Cert          string   `json:"cert"`
	Intermediates *string  `json:"intermediates"`
	ExpDate       string   `json:"exp_date"`
	ListedDomains []string `json:"listed_domains"`
	LetsEncrypt   bool     `json:"is_opalstack_letsencrypt"`
	SHA256        string   `json:"sha256_fingerprint"`
	// The spec gives a certificate no state. When the API sends one it is used.
	State *string `json:"state"`
	Ready *bool   `json:"ready"`
}

// A certificate without a state is taken as ready.
func (c Cert) status() Status {
	if c.Ready == nil {
		return Status{State: "READY", Ready: true}
	}
	st := Status{Ready: *c.Ready}
	if c.State != nil {
		st.State = *c.State
	}
	return st
}

type CertCreate struct {
	Name          string  `json:"name"`
	Cert          string  `json:"cert"`
	Intermediates *string `json:"intermediates,omitempty"`
	Key           string  `json:"key"`
}

// CertUpdate replaces the certificate, and with it usually the key.
type CertUpdate struct {
	ID            string           `json:"id"`
	Name          string           `json:"name,omitempty"`
	Cert          string           `json:"cert,omitempty"`
	Intermediates Nullable[string] `json:"intermediates,omitzero"`
	Key           string           `json:"key,omitempty"`
}

func (c *Client) Certs() Kind[Cert, CertCreate, CertUpdate] {
	return kind[Cert, CertCreate, CertUpdate](c, "cert")
}
