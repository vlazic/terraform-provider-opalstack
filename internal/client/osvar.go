package client

import "encoding/json"

// OSVar is an environment variable of shell users.
type OSVar struct {
	Status
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Content string   `json:"content"`
	OSUsers []string `json:"osusers"`
	// Global marks a variable every shell user of the account gets.
	Global bool `json:"global"`
}

type OSVarCreate struct {
	Name    string   `json:"name"`
	Content string   `json:"content"`
	OSUsers []string `json:"osusers"`
	Global  *bool    `json:"global,omitempty"`
}

// OSVarUpdate has no name: the API does not take one. Fields left nil are not sent.
type OSVarUpdate struct {
	ID      string    `json:"id"`
	Content *string   `json:"content,omitempty"`
	OSUsers *[]string `json:"osusers,omitempty"`
	Global  *bool     `json:"global,omitempty"`
}

func (r OSVarCreate) MarshalJSON() ([]byte, error) {
	type plain OSVarCreate // drops this method, so the call below does not recurse
	r.OSUsers = orEmpty(r.OSUsers)
	return json.Marshal(plain(r))
}

// A variable is judged by its own name for the safety prefix, and by the shell
// users it is given to. A global one is refused outright while the prefix is
// set (guard): it reaches shell users no name can vouch for.
func (c *Client) OSVars() Kind[OSVar, OSVarCreate, OSVarUpdate] {
	k := kind[OSVar, OSVarCreate, OSVarUpdate](c, "osvar")
	k.refs = []ref{{path: "osusers", kind: "osuser", exempt: c.cfg.SafetyOSUserID}}
	return k
}
