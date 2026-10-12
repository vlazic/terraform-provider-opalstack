package client

// Cert is a TLS certificate. The API returns the private key with every read;
// this type has no field for it, so nothing here carries it.
type Cert struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Cert          string  `json:"cert"`
	Intermediates *string `json:"intermediates"`
	ExpDate       string  `json:"exp_date"`
	SHA256        string  `json:"sha256_fingerprint"`
}

// A certificate has no state: the API answers every write with the finished
// object. It is always ready.
func (Cert) status() Status { return Status{State: "READY", Ready: true} }

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
