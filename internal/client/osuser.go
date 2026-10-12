package client

const kindOSUser = "osuser"

// OSUser is a shell user.
type OSUser struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	Ready  bool   `json:"ready"`
	Name   string `json:"name"`
	Server string `json:"server"`
	// DefaultPassword is set only on the value returned by Create. The API
	// chooses it, and no read returns it again.
	DefaultPassword string `json:"default_password,omitempty"`
}

type OSUserCreate struct {
	Server   string `json:"server"`
	Name     string `json:"name"`
	Password string `json:"password,omitempty"`
}

func (r OSUserCreate) guardTarget() guardTarget { return guardTarget{name: r.Name} }

// OSUserUpdate can change the password and nothing else.
type OSUserUpdate struct {
	ID       string `json:"id"`
	Password string `json:"password,omitempty"`
}

func (r OSUserUpdate) objectID() string { return r.ID }

func (c *Client) OSUsers() Kind[OSUser, OSUserCreate, OSUserUpdate] {
	return Kind[OSUser, OSUserCreate, OSUserUpdate]{c: c, name: kindOSUser}
}
