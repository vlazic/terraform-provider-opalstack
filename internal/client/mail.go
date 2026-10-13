package client

import "encoding/json"

// MailUser is a mailbox.
type MailUser struct {
	Status
	ID         string `json:"id"`
	Name       string `json:"name"`
	IMAPServer string `json:"imap_server"`
	// DefaultPassword is set only on the value returned by Create, when no
	// password was sent. No read returns it again.
	DefaultPassword string `json:"default_password,omitempty"`
	MailFilter
}

// MailFilter is what a mailbox does with mail by itself: its procmail rules
// and its autoresponder.
type MailFilter struct {
	Procmailrc           string   `json:"procmailrc"`
	AutoresponderEnable  bool     `json:"autoresponder_enable"`
	AutoresponderSubject string   `json:"autoresponder_subject"`
	AutoresponderMessage string   `json:"autoresponder_message"`
	AutoresponderNoreply []string `json:"autoresponder_noreply"`
}

// MailFilterChange is the part of a create or an update that sets them. A
// field left nil is not sent, and the API then keeps what it has.
type MailFilterChange struct {
	Procmailrc           *string   `json:"procmailrc,omitempty"`
	AutoresponderEnable  *bool     `json:"autoresponder_enable,omitempty"`
	AutoresponderSubject *string   `json:"autoresponder_subject,omitempty"`
	AutoresponderMessage *string   `json:"autoresponder_message,omitempty"`
	AutoresponderNoreply *[]string `json:"autoresponder_noreply,omitempty"`
}

type MailUserCreate struct {
	IMAPServer string `json:"imap_server"`
	Name       string `json:"name"`
	Password   string `json:"password,omitempty"`
	MailFilterChange
}

type MailUserUpdate struct {
	ID       string `json:"id"`
	Password string `json:"password,omitempty"`
	MailFilterChange
}

func (c *Client) MailUsers() Kind[MailUser, MailUserCreate, MailUserUpdate] {
	return kind[MailUser, MailUserCreate, MailUserUpdate](c, "mailuser")
}

// Address is a mail address: mail for Source is delivered to the mailboxes in
// Destinations and sent on to the addresses in Forwards.
type Address struct {
	Status
	ID           string   `json:"id"`
	Source       string   `json:"source"`
	Destinations []string `json:"destinations"`
	Forwards     []string `json:"forwards"`
}

// AddressCreate always sends forwards as an array: the API requires the field.
type AddressCreate struct {
	Source       string   `json:"source"`
	Destinations []string `json:"destinations"`
	Forwards     []string `json:"forwards"`
}

// AddressUpdate replaces both lists: a nil one is sent as empty. The source cannot be changed here: an
// address with another source is another address.
type AddressUpdate struct {
	ID           string   `json:"id"`
	Destinations []string `json:"destinations"`
	Forwards     []string `json:"forwards"`
}

func (r AddressCreate) MarshalJSON() ([]byte, error) {
	type plain AddressCreate // drops this method, so the call below does not recurse
	r.Destinations, r.Forwards = orEmpty(r.Destinations), orEmpty(r.Forwards)
	return json.Marshal(plain(r))
}

func (r AddressUpdate) MarshalJSON() ([]byte, error) {
	type plain AddressUpdate
	r.Destinations, r.Forwards = orEmpty(r.Destinations), orEmpty(r.Forwards)
	return json.Marshal(plain(r))
}

// orEmpty makes a nil list an empty one, which marshals as [] and not as null.
func orEmpty(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// An address has no name. The safety prefix is checked on its source, and on
// the mailboxes it delivers to.
func (c *Client) Addresses() Kind[Address, AddressCreate, AddressUpdate] {
	k := kind[Address, AddressCreate, AddressUpdate](c, "address")
	k.nameField = "source"
	k.refs = []ref{{path: "destinations", kind: "mailuser"}}
	return k
}
