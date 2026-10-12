package client

type DNSRecord struct {
	Status
	ID       string `json:"id"`
	Domain   string `json:"domain"`
	Type     string `json:"type"`
	Content  string `json:"content"`
	Priority int64  `json:"priority"`
	TTL      int64  `json:"ttl"`
}

type DNSRecordCreate struct {
	Domain   string `json:"domain"`
	Type     string `json:"type"`
	Content  string `json:"content"`
	Priority *int64 `json:"priority,omitempty"`
	TTL      *int64 `json:"ttl,omitempty"`
}

// DNSRecordUpdate leaves out domain and type on purpose. The API accepts both,
// but a record that changes zone or type is a different record.
type DNSRecordUpdate struct {
	ID       string `json:"id"`
	Content  string `json:"content,omitempty"`
	Priority *int64 `json:"priority,omitempty"`
	TTL      *int64 `json:"ttl,omitempty"`
}

// A DNS record has no name, so the safety prefix is checked on its domain.
func (c *Client) DNSRecords() Kind[DNSRecord, DNSRecordCreate, DNSRecordUpdate] {
	k := kind[DNSRecord, DNSRecordCreate, DNSRecordUpdate](c, "dnsrecord")
	k.parentField, k.parentKind = "domain", "domain"
	return k
}
