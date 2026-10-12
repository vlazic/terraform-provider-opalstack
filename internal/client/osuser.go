package client

// OSUser is a shell user.
type OSUser struct {
	Status
	ID     string `json:"id"`
	Name   string `json:"name"`
	Server string `json:"server"`
	// DefaultPassword is set only on the value returned by Create. The API
	// chooses it, and no read returns it again.
	DefaultPassword string `json:"default_password,omitempty"`
}

type OSUserCreate struct {
	Server   string `json:"server"`
	Name     string `json:"name,omitempty"`
	Password string `json:"password,omitempty"`
}

// OSUserUpdate can change the password and nothing else.
type OSUserUpdate struct {
	ID       string `json:"id"`
	Password string `json:"password,omitempty"`
}

func (c *Client) OSUsers() Kind[OSUser, OSUserCreate, OSUserUpdate] {
	return kind[OSUser, OSUserCreate, OSUserUpdate](c, "osuser")
}
